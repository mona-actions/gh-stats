// Package stats implements concurrent processing of GitHub repository statistics.
//
// This file (concurrency.go) contains the parallel repository processing implementation.
// It provides a worker pool pattern with semaphore-based concurrency control to safely
// process multiple repositories in parallel while respecting GitHub API rate limits.
//
// Key features:
//   - Controlled parallelism using semaphores
//   - Thread-safe error collection
//   - Batch disk persistence of results (every 10 repos)
//   - Context-aware cancellation support
//   - Panic recovery in worker goroutines
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

// calculateBatchSize determines optimal batch size based on total repository count.
// This balances crash recovery safety (smaller batches = less data loss on crash)
// with I/O performance (larger batches = fewer file writes).
//
// Strategy:
//   - Tiny orgs (< 50 repos): Save once at end (batch size = repo count)
//   - Small orgs (50-500 repos): Save every 50 repos
//   - Medium orgs (500-2000 repos): Save every 100 repos
//   - Large orgs (2000+ repos): Save every 200 repos
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
//  1. Retries up to 3 times with exponential backoff for transient errors
//  2. Falls back to a timestamped recovery file if main file consistently fails
//  3. Returns error only if both main and fallback files fail
//
// This ensures users don't lose data on Ctrl-C, disk full, or other transient failures.
func saveBatchWithRetry(outputFile string, repos []output.RepoStats, org string) error {
	const maxRetries = 3
	const baseRetryDelay = 1 * time.Second

	var lastErr error

	// Try to save to main file with retries
	for attempt := 0; attempt < maxRetries; attempt++ {
		lastErr = output.AppendToConsolidatedJSON(outputFile, nil, repos, nil)

		if lastErr == nil {
			// Success!
			if attempt > 0 {
				pterm.Success.Printf("✓ Saved batch of %d repos after %d attempt(s)\n", len(repos), attempt+1)
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
		pterm.Warning.Printf("  You can manually merge this file later or re-run with --resume\n")
		// Return nil because we saved to fallback - data is not lost
		return nil
	}

	// Both main and fallback failed - this is truly bad
	pterm.Error.Printf("🔥 CRITICAL: Could not save to main file (%v) OR fallback file (%v)\n", lastErr, fallbackErr)
	return fmt.Errorf("failed to save batch to main file: %w (fallback also failed: %v)", lastErr, fallbackErr)
}

// processReposParallel processes repositories concurrently with controlled parallelism.
//
// This function is the core of parallel repository processing. It uses a semaphore pattern
// to limit concurrent API calls and saves results to disk in batches as they complete.
//
// Concurrency model:
//   - Worker pool with config.MaxWorkers concurrent goroutines
//   - Semaphore (buffered channel) controls parallelism
//   - Mutex protects shared slices (stats, errors)
//   - Separate mutex for progress bar (pterm is not thread-safe)
//   - Background goroutine updates progress bar with rate limit info every 2 seconds
//
// Error handling:
//   - Individual repository failures are collected but don't stop processing
//   - Context cancellation stops all in-flight requests
//   - All errors are returned at the end for caller to decide next action
//
// Parameters:
//   - ctx: Context for cancellation
//   - org: Organization name
//   - repoNames: List of repository names to process
//   - packageData: Pre-fetched package data (may be empty)
//   - teamAccessMap: Pre-fetched team access data (may be empty)
//   - progress: Progress bar for visual feedback
//   - config: Configuration with feature flags and worker count
//   - outputFile: Path to save results incrementally
//
// Returns:
//   - stats: Successfully processed repository statistics
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

// processRepoWorker processes a single repository with error handling and panic recovery.
func processRepoWorker(
	ctx context.Context,
	org, repoName string,
	packageData map[string]output.PackageCounts,
	teamAccessMap map[string][]output.RepoTeamAccess,
	config Config,
	stats *[]output.RepoStats,
	errors *[]error,
	saveBatch *[]output.RepoStats,
	batchSize int,
	outputFile string,
	mu, saveBatchMu *sync.Mutex,
	progressUpdates chan<- progressUpdate,
) {
	// Panic recovery
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			mu.Lock()
			*errors = append(*errors, fmt.Errorf("%s/%s: panic recovered: %v\nStack:\n%s", org, repoName, r, stack))
			mu.Unlock()
			pterm.Error.Printf("🔥 PANIC in worker processing %s/%s: %v\n", org, repoName, r)
			pterm.Error.Printf("Stack trace:\n%s\n", stack)
		}
	}()

	// Check for cancellation
	select {
	case <-ctx.Done():
		mu.Lock()
		*errors = append(*errors, fmt.Errorf("%s/%s: %w", org, repoName, ctx.Err()))
		mu.Unlock()
		return
	default:
	}

	// Build fetch config
	fetchConfig := ghapi.DataFetchConfig{
		FetchSettings:     config.FetchSettings,
		FetchCustomProps:  config.FetchCustomProps,
		FetchBranches:     config.FetchBranches,
		FetchWebhooks:     config.FetchWebhooks,
		FetchAutolinks:    config.FetchAutolinks,
		FetchActions:      config.FetchActions,
		FetchSecurity:     config.FetchSecurity,
		FetchPages:        config.FetchPages,
		FetchIssuesData:   config.FetchIssuesData,
		FetchPRsData:      config.FetchPRsData,
		FetchTraffic:      config.FetchTraffic,
		FetchTags:         config.FetchTags,
		FetchGitRefs:      config.FetchGitRefs,
		FetchLFS:          config.FetchLFS,
		FetchFiles:        config.FetchFiles,
		FetchContributors: config.FetchContributors,
		FetchCommits:      config.FetchCommits,
		FetchIssueEvents:  config.FetchIssueEvents,
	}

	// Process the repository
	repoStats, err := ghapi.GetRepoDetails(ctx, org, repoName, packageData, teamAccessMap, fetchConfig)

	// Update stats/errors
	mu.Lock()
	if err != nil {
		*errors = append(*errors, fmt.Errorf("%s/%s: %w", org, repoName, err))
	} else {
		*stats = append(*stats, repoStats)
	}
	mu.Unlock()

	// Handle result
	if err != nil {
		state.Get().PrintRepo(repoName, false, err.Error())
	} else {
		state.Get().PrintRepo(repoName, true, "")

		// Add to batch and check if we should flush
		saveBatchMu.Lock()
		*saveBatch = append(*saveBatch, repoStats)
		shouldFlush := len(*saveBatch) >= batchSize
		var toSave []output.RepoStats
		if shouldFlush {
			toSave = make([]output.RepoStats, len(*saveBatch))
			copy(toSave, *saveBatch)
			*saveBatch = (*saveBatch)[:0]
		}
		saveBatchMu.Unlock()

		// Flush batch if needed
		if shouldFlush && len(toSave) > 0 {
			if saveErr := saveBatchWithRetry(outputFile, toSave, org); saveErr != nil {
				mu.Lock()
				for _, repo := range toSave {
					*errors = append(*errors, fmt.Errorf("%s/%s: failed to save to disk after retries: %w", repo.Org, repo.Name, saveErr))
				}
				mu.Unlock()
				pterm.Error.Printf("🔥 CRITICAL: Lost batch of %d repos due to save failure\n", len(toSave))
			}
		}
	}

	progressUpdates <- progressUpdate{delta: 1}
}

// processReposParallel processes repositories concurrently with controlled parallelism.
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

	batchSize := calculateBatchSize(len(repoNames))
	if config.Verbose {
		estimatedSaves := (len(repoNames) + batchSize - 1) / batchSize
		pterm.Debug.Printf("Using dynamic batch size: %d repos per save (~%d saves total)\n",
			batchSize, estimatedSaves)
	}

	saveBatch := make([]output.RepoStats, 0, batchSize)
	var mu, saveBatchMu sync.Mutex

	sem := make(chan struct{}, config.MaxWorkers)
	var wg sync.WaitGroup
	done := make(chan struct{})

	progressUpdates := make(chan progressUpdate, config.MaxWorkers*4)
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

	// Start background progress updater
	updaterDone := startProgressUpdater(done, progress, progressUpdates, len(repoNames))

	// Process repositories in parallel
spawnLoop:
	for _, repoName := range repoNames {
		// Check for cancellation before spawning new workers
		select {
		case <-ctx.Done():
			// Stop spawning new workers if context is cancelled
			break spawnLoop
		default:
		}

		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			processRepoWorker(ctx, org, name, packageData, teamAccessMap, config,
				&stats, &errors, &saveBatch, batchSize, outputFile,
				&mu, &saveBatchMu, progressUpdates)
		}(repoName)
	}

	wg.Wait()

	// Flush remaining batch
	saveBatchMu.Lock()
	finalBatch := make([]output.RepoStats, len(saveBatch))
	copy(finalBatch, saveBatch)
	saveBatchMu.Unlock()

	if len(finalBatch) > 0 {
		if saveErr := saveBatchWithRetry(outputFile, finalBatch, org); saveErr != nil {
			pterm.Error.Printf("🔥 CRITICAL: Failed to save final batch of %d repos\n", len(finalBatch))
			mu.Lock()
			for _, repo := range finalBatch {
				errors = append(errors, fmt.Errorf("%s/%s: failed to save final batch to disk: %w", repo.Org, repo.Name, saveErr))
			}
			mu.Unlock()
		}
	}

	close(done)
	<-updaterDone
	close(progressUpdates)
	progressHandler.Wait()

	return stats, errors
}
