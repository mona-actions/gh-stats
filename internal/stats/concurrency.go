// Package stats implements concurrent processing of GitHub repository statistics.
//
// This file (concurrency.go) contains the parallel repository processing implementation.
// It provides a worker pool pattern with semaphore-based concurrency control to safely.
// process multiple repositories in parallel while respecting GitHub API rate limits.
//
// Key features:
//   - Controlled parallelism using semaphores.
//   - Thread-safe error collection.
//   - Batch disk persistence of results (every 10 repos)
//   - Context-aware cancellation support.
//   - Panic recovery in worker goroutines.
package stats

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/mona-actions/gh-stats/internal/ghapi"
	"github.com/mona-actions/gh-stats/internal/output"
	"github.com/mona-actions/gh-stats/internal/state"
	"github.com/pterm/pterm"
)

type progressUpdate struct {
	delta int
	title string
}

// appendError is a thread-safe helper to append formatted errors to the error slice.
func appendError(mu *sync.Mutex, errors *[]error, org, repo string, err error) {
	mu.Lock()
	*errors = append(*errors, fmt.Errorf("%s/%s: %w", org, repo, err))
	mu.Unlock()
}

// calculateBatchSize determines optimal batch size based on total repository count.
// This balances crash recovery safety (smaller batches = less data loss on crash)
// with I/O performance (larger batches = fewer file writes).
//
// Strategy:
//   - Tiny orgs (< 50 repos): Save once at end (batch size = repo count)
//   - Small orgs (50-500 repos): Save every 50 repos.
//   - Medium orgs (500-2000 repos): Save every 100 repos.
//   - Large orgs (2000+ repos): Save every 200 repos.
//
// Examples:
//   - 10 repos → batch size 10 (1 save at end)
//   - 100 repos → batch size 50 (2 saves)
//   - 1000 repos → batch size 100 (10 saves)
//   - 5000 repos → batch size 200 (25 saves)
func calculateBatchSize(totalRepos int) int {
	switch {
	case totalRepos < 50:
		// Small orgs: save everything at once (minimize I/O)
		return totalRepos
	case totalRepos < 500:
		// Medium orgs: save every 50 repos (2-10 saves total)
		return 50
	case totalRepos < 2000:
		// Large orgs: save every 100 repos (5-20 saves total)
		return 100
	default:
		// Huge orgs: save every 200 repos (10+ saves total)
		return 200
	}
}

// saveBatchWithRetry attempts to save a batch of repo stats with retry logic and fallback file.
// This function provides multiple layers of protection against data loss:
//  1. Retries up to 3 times with exponential backoff for transient errors.
//  2. Falls back to a timestamped recovery file if main file consistently fails.
//  3. Returns error only if both main and fallback files fail.
//
// This ensures users don't lose data on Ctrl-C, disk full, or other transient failures.
// When silent is true, success messages are suppressed (used during individual processing to avoid interrupting tree output).
func saveBatchWithRetry(outputFile string, repos []output.RepoStats, org string, silent bool) error {
	const maxRetries = 3
	const baseRetryDelay = 1 * time.Second

	var lastErr error

	// Try to save to main file with retries
	for attempt := 0; attempt < maxRetries; attempt++ {
		lastErr = output.AppendToConsolidatedJSON(outputFile, nil, repos, nil)

		if lastErr == nil {
			// Success!
			if !silent {
				if attempt > 0 {
					pterm.Success.Printf("✓ Saved batch of %d repos after %d attempt(s)\n", len(repos), attempt+1)
				} else {
					pterm.Info.Printf("💾 Saved batch of %d repos to disk\n", len(repos))
				}
			}
			return nil
		}

		// Save failed, check if we should retry
		if attempt < maxRetries-1 {
			retryDelay := baseRetryDelay * time.Duration(1<<uint(attempt)) // Exponential backoff: 1s, 2s, 4s
			pterm.Warning.Printf("⚠️  Save attempt %d/%d failed: %v (retrying in %v)\n",
				attempt+1, maxRetries, lastErr, retryDelay)
			time.Sleep(retryDelay)
		}
	}

	// Main file failed after all retries - try fallback file
	pterm.Warning.Printf("⚠️  Main file save failed after %d attempts, trying fallback file...\n", maxRetries)

	fallbackFile := fmt.Sprintf("%s.recovery-%s-%s.json",
		outputFile,
		org,
		time.Now().Format("20060102-150405"))

	fallbackErr := output.WriteConsolidatedJSON(fallbackFile, output.ConsolidatedStats{
		Orgs:     []output.OrgMetadata{},
		Repos:    repos,
		Packages: []output.PackageData{},
	})

	if fallbackErr == nil {
		pterm.Warning.Printf("✓ Saved batch to recovery file: %s\n", fallbackFile)
		pterm.Warning.Printf("  You can manually merge this file later or re-run to automatically resume\n")
		// Return nil because we saved to fallback - data is not lost
		return nil
	}

	// Both main and fallback failed - this is truly bad
	pterm.Error.Printf("🔥 CRITICAL: Could not save to main file (%v) OR fallback file (%v)\n", lastErr, fallbackErr)
	return fmt.Errorf("failed to save batch to main file: %w (fallback also failed: %v)", lastErr, fallbackErr)
}

// processReposParallel processes repositories concurrently with controlled parallelism.
//
// This function is the core of parallel repository processing. It uses a semaphore pattern.
// to limit concurrent API calls and saves results to disk in batches as they complete.
//
// Concurrency model:
//   - Worker pool with config.MaxWorkers concurrent goroutines.
//   - Semaphore (buffered channel) controls parallelism.
//   - Mutex protects shared slices (stats, errors)
//   - Separate mutex for progress bar (pterm is not thread-safe)
//   - Background goroutine updates progress bar with rate limit info every 2 seconds.
//
// Error handling:
//   - Individual repository failures are collected but don't stop processing.
//   - Context cancellation stops all in-flight requests.
//   - All errors are returned at the end for caller to decide next action.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - org: Organization name.
//   - repoNames: List of repository names to process.
//   - packageData: Pre-fetched package data (may be empty)
//   - teamAccessMap: Pre-fetched team access data (may be empty)
//   - progress: Progress bar for visual feedback.
//   - config: Configuration with feature flags and worker count.
//   - outputFile: Path to save results incrementally.
//
// Returns:
//   - stats: Successfully processed repository statistics.
//   - errors: All errors encountered (with repo context)
//
// startProgressUpdater starts a background goroutine to update progress bar with rate limit info.
func startProgressUpdater(done <-chan struct{}, progress *pterm.ProgressbarPrinter, updates chan<- progressUpdate, repoCount int) <-chan struct{} {
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				rateLimit := state.Get().GetRateLimit()
				graphqlLimit := state.Get().GetGraphQLRateLimit()

				var title string
				if rateLimit.Limit > 0 && graphqlLimit.Limit > 0 {
					restUsed := rateLimit.Limit - rateLimit.Remaining
					title = fmt.Sprintf("Processing %d repositories... [REST: %d/%d - GQL: %d/%d]",
						repoCount, restUsed, rateLimit.Limit, graphqlLimit.Used, graphqlLimit.Limit)
				} else if rateLimit.Limit > 0 {
					used := rateLimit.Limit - rateLimit.Remaining
					title = fmt.Sprintf("Processing %d repositories... [API: %d/%d used, %d remaining]",
						repoCount, used, rateLimit.Limit, rateLimit.Remaining)
				}

				if title != "" {
					select {
					case updates <- progressUpdate{title: title}:
					default:
					}
				}
			}
		}
	}()
	return stopped
}

// processReposParallel processes repositories concurrently with controlled parallelism.
// Uses GraphQL batching to reduce API calls by fetching multiple repos per query.
func processReposParallel(
	ctx context.Context,
	org string,
	repoNames []string,
	packageData map[string]output.PackageCounts,
	teamAccessMap map[string][]output.RepoTeamAccess,
	progress *pterm.ProgressbarPrinter,
	config Config,
	outputFile string,
) ([]output.RepoStats, []error) {
	stats := make([]output.RepoStats, 0, len(repoNames))
	errors := make([]error, 0, len(repoNames)/5)

	diskBatchSize := calculateBatchSize(len(repoNames))
	if config.Verbose {
		estimatedSaves := (len(repoNames) + diskBatchSize - 1) / diskBatchSize
		pterm.Debug.Printf("Using dynamic batch size: %d repos per save (~%d saves total)\n",
			diskBatchSize, estimatedSaves)
	}

	graphQLBatchSize := validateGraphQLBatchSize(config.GraphQLBatchSize)
	if config.Verbose {
		estimatedGraphQLCalls := (len(repoNames) + graphQLBatchSize - 1) / graphQLBatchSize
		pterm.Debug.Printf("GraphQL batch size: %d repos per query (~%d GraphQL calls vs %d without batching)\n",
			graphQLBatchSize, estimatedGraphQLCalls, len(repoNames))
	}

	saveBatch := make([]output.RepoStats, 0, diskBatchSize)
	var mu, saveBatchMu sync.Mutex

	done := make(chan struct{})
	progressUpdates := make(chan progressUpdate, config.MaxWorkers*4)

	// Start progress handler
	progressHandler := startProgressHandler(progress, progressUpdates)

	// Start background progress updater
	updaterDone := startProgressUpdater(done, progress, progressUpdates, len(repoNames))

	// Build fetch config
	fetchConfig := buildFetchConfig(config)

	// Determine mode name for display based on what's enabled
	modeName := determineModeName(fetchConfig)

	// Add spacing before repository processing section
	pterm.Println()

	// Print repository processing header
	output.PrintRepoProcessingHeader(len(repoNames), modeName)

	// STEP 1: Fetch base GraphQL data for ALL repos in batches (with incremental saves)
	// This already saves data to disk incrementally
	if !config.Verbose {
		pterm.Info.Println("🔄 Fetching repository data via GraphQL batches...")
	}
	allBasicRepoData, batchStats := fetchAllBasicRepoData(ctx, org, repoNames, graphQLBatchSize, diskBatchSize,
		outputFile, fetchConfig, config.Verbose, &errors, &mu)

	// STEP 2: Only process repos individually if there's additional data to fetch
	// In minimal mode with batching, we already have everything we need!
	needsProcessing := needsIndividualProcessing(fetchConfig)

	if needsProcessing {
		if config.Verbose {
			pterm.Debug.Println("Additional processing needed - fetching REST/expensive GraphQL fields...")
		}
		// Process each repo individually with MaxWorkers parallelism
		processReposWithWorkers(ctx, org, allBasicRepoData, packageData, teamAccessMap, fetchConfig, outputFile,
			diskBatchSize, config.MaxWorkers, &stats, &errors, &saveBatch, &mu, &saveBatchMu, progressUpdates)

		// Flush remaining batch
		flushFinalBatch(outputFile, org, saveBatch, &saveBatchMu, &mu, &errors)
	} else {
		// Use the stats we already collected during batch fetching
		stats = batchStats
		// Update progress for all repos (they're already done!)
		progressUpdates <- progressUpdate{delta: len(repoNames)}

		// Print skip message
		output.PrintProcessingSkipped("--minimal")
	}

	// Signal updater to stop
	close(done)
	<-updaterDone
	close(progressUpdates)
	progressHandler.Wait()

	return stats, errors
}

// validateGraphQLBatchSize ensures the batch size is within acceptable limits.
func validateGraphQLBatchSize(batchSize int) int {
	if batchSize < 1 {
		return 20 // default
	}
	if batchSize > 50 {
		return 50 // max to prevent query complexity issues
	}
	return batchSize
}

// needsIndividualProcessing determines if we need to process repos individually
// after batch fetching, or if batch fetching captured everything.
func needsIndividualProcessing(config ghapi.DataFetchConfig) bool {
	// REST API-only fields that aren't in GraphQL batches
	hasRESTFields := hasAnyRESTFields(config)

	// Expensive GraphQL fields fetched individually (not in batch)
	hasExpensiveFields := hasAnyExpensiveGraphQLFields(config)

	return hasRESTFields || hasExpensiveFields
}

// hasAnyRESTFields checks if any REST API-only fields are enabled.
func hasAnyRESTFields(config ghapi.DataFetchConfig) bool {
	return config.FetchBranches || config.FetchWebhooks || config.FetchAutolinks ||
		config.FetchActions || config.FetchSecurity || config.FetchPages || config.FetchTraffic ||
		config.FetchTags || config.FetchGitRefs || config.FetchLFS || config.FetchFiles ||
		config.FetchCommits || config.FetchIssueEvents
}

// hasAnyExpensiveGraphQLFields checks if any expensive GraphQL fields are enabled.
func hasAnyExpensiveGraphQLFields(config ghapi.DataFetchConfig) bool {
	return config.FetchCollaborators || config.FetchRulesets || config.FetchMilestones ||
		config.FetchDeployKeys || config.FetchEnvironments || config.FetchDeployments ||
		config.FetchPRsData || config.FetchIssuesData || config.FetchContributors ||
		config.FetchReleases || config.FetchBranchProtection || config.FetchCommunityFiles
}

// determineModeName returns a descriptive mode name based on enabled features.
// Limits display to top 2-3 features to avoid breaking formatting with long descriptions.
func determineModeName(config ghapi.DataFetchConfig) string {
	hasREST := hasAnyRESTFields(config)
	hasExpensive := hasAnyExpensiveGraphQLFields(config)

	// Minimal mode: only base GraphQL batch fields
	if !hasREST && !hasExpensive {
		return "--minimal"
	}

	// Full mode: everything enabled
	if isAllRESTEnabled(config) && isAllExpensiveEnabled(config) {
		return "full"
	}

	// Custom mode: build description of what's enabled
	features := collectEnabledFeatures(config)
	return formatCustomModeName(features)
}

// isAllRESTEnabled checks if all REST API fields are enabled.
func isAllRESTEnabled(config ghapi.DataFetchConfig) bool {
	return config.FetchBranches && config.FetchWebhooks && config.FetchAutolinks &&
		config.FetchActions && config.FetchSecurity && config.FetchPages && config.FetchTraffic &&
		config.FetchTags && config.FetchGitRefs && config.FetchLFS && config.FetchFiles &&
		config.FetchCommits && config.FetchIssueEvents
}

// isAllExpensiveEnabled checks if all expensive GraphQL fields are enabled.
func isAllExpensiveEnabled(config ghapi.DataFetchConfig) bool {
	return config.FetchCollaborators && config.FetchRulesets && config.FetchMilestones &&
		config.FetchDeployKeys && config.FetchEnvironments && config.FetchDeployments &&
		config.FetchPRsData && config.FetchIssuesData && config.FetchContributors &&
		config.FetchReleases && config.FetchBranchProtection && config.FetchCommunityFiles
}

// collectEnabledFeatures collects high-level feature categories that are enabled.
func collectEnabledFeatures(config ghapi.DataFetchConfig) []string {
	features := make([]string, 0, 5)

	if config.FetchPRsData || config.FetchIssuesData {
		features = append(features, "issues/PRs")
	}
	if config.FetchContributors || config.FetchCollaborators {
		features = append(features, "contributors")
	}
	if config.FetchCommits || config.FetchReleases {
		features = append(features, "commits/releases")
	}
	if config.FetchSecurity || config.FetchRulesets || config.FetchBranchProtection {
		features = append(features, "security")
	}
	if config.FetchActions || config.FetchWebhooks || config.FetchEnvironments || config.FetchDeployments {
		features = append(features, "CI/CD")
	}

	return features
}

// formatCustomModeName formats the custom mode name with enabled features.
func formatCustomModeName(features []string) string {
	if len(features) == 0 {
		return "custom"
	}
	if len(features) == 1 {
		return "custom (" + features[0] + ")"
	}
	if len(features) == 2 {
		return "custom (" + features[0] + " + " + features[1] + ")"
	}
	// 3+ features: show first 2 + count
	remaining := len(features) - 2
	return fmt.Sprintf("custom (%s + %s + %d more)", features[0], features[1], remaining)
}

// startProgressHandler starts a goroutine to handle progress updates.
func startProgressHandler(progress *pterm.ProgressbarPrinter, progressUpdates <-chan progressUpdate) *sync.WaitGroup {
	var progressHandler sync.WaitGroup
	progressHandler.Add(1)
	go func() {
		defer progressHandler.Done()
		for update := range progressUpdates {
			if update.title != "" {
				progress.UpdateTitle(update.title)
			}
			if update.delta > 0 {
				for i := 0; i < update.delta; i++ {
					progress.Increment()
				}
			}
		}
	}()
	return &progressHandler
}

// processBatchRepoData processes a single repo's basic data and adds it to collections.
func processBatchRepoData(org, repoName string, basicData map[string]interface{},
	allRepoStats *[]output.RepoStats, saveBatch *[]output.RepoStats, diskBatchSize int) bool {

	// Convert basic data to minimal RepoStats
	repoStats := convertBasicDataToRepoStats(org, repoName, basicData)
	*allRepoStats = append(*allRepoStats, repoStats)
	*saveBatch = append(*saveBatch, repoStats)

	// Check if batch is full
	return len(*saveBatch) >= diskBatchSize
}

// saveBatchToDisk saves the current batch to disk and clears it.
func saveBatchToDisk(outputFile, org string, saveBatch *[]output.RepoStats,
	saveBatchMu, mu *sync.Mutex, errors *[]error) {

	saveBatchMu.Lock()
	if len(*saveBatch) == 0 {
		saveBatchMu.Unlock()
		return
	}

	toSave := make([]output.RepoStats, len(*saveBatch))
	copy(toSave, *saveBatch)
	*saveBatch = (*saveBatch)[:0] // Clear the batch
	saveBatchMu.Unlock()

	// Save outside the lock (not silent - this is initial batch fetch, user should see progress)
	if saveErr := saveBatchWithRetry(outputFile, toSave, org, false); saveErr != nil {
		pterm.Error.Printf("🔥 CRITICAL: Failed to save batch during base data fetch\n")
		mu.Lock()
		for _, repo := range toSave {
			*errors = append(*errors, fmt.Errorf("%s/%s: failed to save batch to disk: %w", repo.Org, repo.Name, saveErr))
		}
		mu.Unlock()
	}
}

// buildFetchConfig creates a DataFetchConfig from the Config.
func buildFetchConfig(config Config) ghapi.DataFetchConfig {
	// Simply return the embedded DataFetchConfig
	return config.DataFetchConfig
}

// fetchAllBasicRepoData fetches base GraphQL data for all repos in batches.
// Saves incremental progress to disk to prevent data loss on interruption.
// Returns both the raw GraphQL data map AND the converted RepoStats array.
func fetchAllBasicRepoData(ctx context.Context, org string, repoNames []string, graphQLBatchSize, diskBatchSize int,
	outputFile string, fetchConfig ghapi.DataFetchConfig, verbose bool, errors *[]error, mu *sync.Mutex) (map[string]map[string]interface{}, []output.RepoStats) {

	if verbose {
		pterm.Debug.Printf("Fetching base GraphQL data for %d repos in batches of %d...\n", len(repoNames), graphQLBatchSize)
	}

	allBasicRepoData := make(map[string]map[string]interface{})
	allRepoStats := make([]output.RepoStats, 0, len(repoNames))
	repoBatches := groupReposIntoBatches(repoNames, graphQLBatchSize)

	// Track repos for incremental saving
	saveBatch := make([]output.RepoStats, 0, diskBatchSize)
	var saveBatchMu sync.Mutex

	// Track batch retries for display
	batchRetries := make(map[int]int)

	for batchIdx, batch := range repoBatches {
		// Check for cancellation
		select {
		case <-ctx.Done():
			for _, name := range batch {
				appendError(mu, errors, org, name, ctx.Err())
			}
			continue
		default:
		}

		if verbose {
			pterm.Debug.Printf("Fetching GraphQL batch %d/%d (%d repos)...\n", batchIdx+1, len(repoBatches), len(batch))
		}

		basicRepoDataMap, failedRepos, err := ghapi.GetBatchedRepoBasicDetails(ctx, org, batch, fetchConfig)
		if err != nil {
			for _, name := range batch {
				appendError(mu, errors, org, name, fmt.Errorf("batch GraphQL query failed: %w", err))
			}
			pterm.Error.Printf("✗ Batch %d GraphQL query failed: %v\n", batchIdx+1, err)
			continue
		}

		// Add successful repos to the map AND prepare for incremental save
		for repoName, basicData := range basicRepoDataMap {
			allBasicRepoData[repoName] = basicData

			// Process and add to save batch
			saveBatchMu.Lock()
			shouldSave := processBatchRepoData(org, repoName, basicData, &allRepoStats, &saveBatch, diskBatchSize)
			saveBatchMu.Unlock()

			// Save incrementally when batch is full
			if shouldSave {
				saveBatchToDisk(outputFile, org, &saveBatch, &saveBatchMu, mu, errors)
			}
		}

		// Print batch completion (non-verbose)
		if !verbose {
			output.PrintBatchProgress(output.BatchProgress{
				Current:   batchIdx + 1,
				Total:     len(repoBatches),
				RepoCount: len(batch),
				Retries:   batchRetries[batchIdx],
				Saved:     true,
			})
		}

		// Handle failed repos
		if len(failedRepos) > 0 {
			for _, name := range failedRepos {
				appendError(mu, errors, org, name, fmt.Errorf("not found in batch response"))
			}
		}
	}

	// Save any remaining repos from the last batch
	saveBatchToDisk(outputFile, org, &saveBatch, &saveBatchMu, mu, errors)

	if verbose {
		pterm.Debug.Printf("Fetched base data for %d repos, now processing individually...\n", len(allBasicRepoData))
	}

	return allBasicRepoData, allRepoStats
}

// convertBasicDataToRepoStats converts raw GraphQL basic data to a minimal RepoStats structure.
// This is used for incremental saving during the batch fetch phase.
// The data will be enriched later in the processing phase.
func convertBasicDataToRepoStats(org, repoName string, basicData map[string]interface{}) output.RepoStats {
	stats := output.RepoStats{
		Org:  org,
		Name: repoName,
	}

	// Extract simple string fields
	if val, ok := basicData["url"].(string); ok {
		stats.URL = val
	}

	// Extract boolean fields
	if val, ok := basicData["isArchived"].(bool); ok {
		stats.IsArchived = val
	}
	if val, ok := basicData["isFork"].(bool); ok {
		stats.IsFork = val
	}
	if val, ok := basicData["hasWikiEnabled"].(bool); ok {
		stats.HasWiki = val
	}

	// Extract numeric fields (GitHub returns float64 for numbers in JSON)
	if val, ok := basicData["stargazerCount"].(float64); ok {
		stats.Stars = int(val)
	}
	if val, ok := basicData["watchers"].(map[string]interface{}); ok {
		if count, ok := val["totalCount"].(float64); ok {
			stats.Watchers = int(count)
		}
	}
	if val, ok := basicData["forkCount"].(float64); ok {
		stats.Forks = int(val)
	}
	if val, ok := basicData["diskUsage"].(float64); ok {
		stats.SizeMB = int(val) / 1024 // Convert KB to MB
	}

	// Extract timestamp strings
	if val, ok := basicData["createdAt"].(string); ok {
		stats.CreatedAt = val
	}
	if val, ok := basicData["updatedAt"].(string); ok {
		stats.UpdatedAt = val
	}
	if val, ok := basicData["pushedAt"].(string); ok {
		stats.PushedAt = val
	}

	// Note: Complex fields (Settings, Languages, Topics, etc.) will be populated
	// later during the full processing phase in processReposWithWorkers.
	// This minimal save ensures we don't lose basic repo data if interrupted.

	return stats
}

// processReposWithWorkers processes repos in parallel using a worker pool.
func processReposWithWorkers(ctx context.Context, org string, allBasicRepoData map[string]map[string]interface{},
	packageData map[string]output.PackageCounts, teamAccessMap map[string][]output.RepoTeamAccess,
	fetchConfig ghapi.DataFetchConfig, outputFile string, diskBatchSize, maxWorkers int,
	stats *[]output.RepoStats, errors *[]error, saveBatch *[]output.RepoStats,
	mu, saveBatchMu *sync.Mutex, progressUpdates chan<- progressUpdate) {

	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup

	for repoName, basicData := range allBasicRepoData {
		// Check for cancellation
		select {
		case <-ctx.Done():
			appendError(mu, errors, org, repoName, ctx.Err())
			progressUpdates <- progressUpdate{delta: 1}
			continue
		default:
		}

		wg.Add(1)
		go processRepoWorker(ctx, org, repoName, basicData, packageData, teamAccessMap, fetchConfig,
			outputFile, diskBatchSize, &wg, sem, stats, errors, saveBatch, mu, saveBatchMu, progressUpdates)
	}

	wg.Wait()
}

// processRepoWorker processes a single repository in a worker goroutine.
func processRepoWorker(ctx context.Context, org, repoName string, basicData map[string]interface{},
	packageData map[string]output.PackageCounts, teamAccessMap map[string][]output.RepoTeamAccess,
	fetchConfig ghapi.DataFetchConfig, outputFile string, diskBatchSize int,
	wg *sync.WaitGroup, sem chan struct{}, stats *[]output.RepoStats, errors *[]error,
	saveBatch *[]output.RepoStats, mu, saveBatchMu *sync.Mutex, progressUpdates chan<- progressUpdate) {

	defer wg.Done()
	sem <- struct{}{}        // Acquire worker slot
	defer func() { <-sem }() // Release worker slot

	// Panic recovery
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			appendError(mu, errors, org, repoName, fmt.Errorf("panic during processing: %v\nStack:\n%s", r, stack))
			pterm.Error.Printf("🔥 PANIC processing %s/%s: %v\n", org, repoName, r)
			progressUpdates <- progressUpdate{delta: 1}
		}
	}()

	// Process this repo with REST + expensive GraphQL
	repoStats, err := ghapi.ProcessRepoWithBasicData(ctx, org, repoName, basicData, packageData, teamAccessMap, fetchConfig)
	if err != nil {
		appendError(mu, errors, org, repoName, fmt.Errorf("failed to process repo: %w", err))
		state.Get().PrintRepo(repoName, false, fmt.Sprintf("processing error: %v", err))
		progressUpdates <- progressUpdate{delta: 1}
		return
	}

	// Successfully processed - add to results
	mu.Lock()
	*stats = append(*stats, repoStats)
	mu.Unlock()

	// Add to save batch (thread-safe)
	saveBatchMu.Lock()
	*saveBatch = append(*saveBatch, repoStats)
	shouldSave := len(*saveBatch) >= diskBatchSize
	var toSave []output.RepoStats
	if shouldSave {
		toSave = make([]output.RepoStats, len(*saveBatch))
		copy(toSave, *saveBatch)
		*saveBatch = (*saveBatch)[:0]
	}
	saveBatchMu.Unlock()

	// Save batch if needed (silent during worker processing to avoid interrupting tree output)
	if shouldSave {
		if saveErr := saveBatchWithRetry(outputFile, toSave, org, true); saveErr != nil {
			mu.Lock()
			for _, repo := range toSave {
				*errors = append(*errors, fmt.Errorf("%s/%s: failed to save to disk: %w", repo.Org, repo.Name, saveErr))
			}
			mu.Unlock()
			pterm.Error.Printf("🔥 CRITICAL: Lost batch of %d repos due to save failure\n", len(toSave))
		}
	}

	// Print repo completion status (immediately!)
	state.Get().PrintRepo(repoName, true, "")
	progressUpdates <- progressUpdate{delta: 1}
}

// flushFinalBatch saves any remaining repos in the save batch.
func flushFinalBatch(outputFile, org string, saveBatch []output.RepoStats, saveBatchMu, mu *sync.Mutex, errors *[]error) {
	saveBatchMu.Lock()
	defer saveBatchMu.Unlock()

	if len(saveBatch) > 0 {
		if saveErr := saveBatchWithRetry(outputFile, saveBatch, org, false); saveErr != nil {
			pterm.Error.Printf("🔥 CRITICAL: Failed to save final batch of %d repos\n", len(saveBatch))
			mu.Lock()
			for _, repo := range saveBatch {
				*errors = append(*errors, fmt.Errorf("%s/%s: failed to save final batch to disk: %w", repo.Org, repo.Name, saveErr))
			}
			mu.Unlock()
		}
	}
}

// groupReposIntoBatches splits repository names into batches of the specified size.
func groupReposIntoBatches(repoNames []string, batchSize int) [][]string {
	batches := make([][]string, 0, (len(repoNames)+batchSize-1)/batchSize)

	for i := 0; i < len(repoNames); i += batchSize {
		end := i + batchSize
		if end > len(repoNames) {
			end = len(repoNames)
		}
		batches = append(batches, repoNames[i:end])
	}

	return batches
}
