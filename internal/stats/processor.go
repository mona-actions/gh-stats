// Package stats implements concurrent processing of GitHub repository statistics.
//
// This file (processor.go) contains the main orchestration logic for stats collection.
// It coordinates the entire data collection process from validation through final output,
// managing multiple organizations, rate limits, and error handling.
//
// Key features:
//   - Multi-organization sequential processing
//   - Per-organization parallel repository processing
//   - Automatic resume from interrupted operations
//   - Dry-run mode for preview and API estimation
//   - Rate limit checking and reporting
//   - Comprehensive error aggregation
//   - Context-aware cancellation support
package stats

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mona-actions/gh-stats/internal/ghapi"
	"github.com/mona-actions/gh-stats/internal/output"
	"github.com/mona-actions/gh-stats/internal/state"
	"github.com/pterm/pterm"
)

const (
	minAPICallsForOrgProcessing = 20 // Minimum calls needed to safely start org processing
)

// Config holds all configuration options for the stats collection.
//
// This struct is used to configure the behavior of the statistics collection process.
// All boolean feature flags default to false (disabled) when zero-valued. Use the
// command-line flag system or explicitly set fields to enable features.
//
// Zero value behavior:
//   - String fields: Empty strings are validated and will cause errors
//   - MaxWorkers: Zero defaults to 3 workers in RunWithContext
//   - Boolean flags: false means feature is disabled
//   - Fetch* flags: false means skip that data collection (faster, fewer API calls)
//
// Example:
//
//	config := stats.Config{
//	    OrgName:    "myorg",
//	    OutputFile: "stats.json",
//	    MaxWorkers: 5,
//	    FetchActions: true,  // Enable Actions data collection
//	    FetchSecurity: true, // Enable security data collection
//	}
type Config struct {
	OrgName    string // Organization name to analyze (mutually exclusive with InputFile)
	InputFile  string // File with list of organizations to analyze (mutually exclusive with OrgName)
	OutputFile string // Output file path for results (JSON format)
	MaxWorkers int    // Maximum number of concurrent API calls (default: 3)
	FailFast   bool   // Stop processing on first error
	Verbose    bool   // Enable verbose output and debug information
	Resume     bool   // Resume from existing output file, skipping already processed repositories
	DryRun     bool   // Show what would be collected without making API calls (preview mode)
	NoPackages bool   // Skip fetching package data for faster execution
	Hostname   string // GitHub Enterprise Server hostname (e.g., github.company.com)

	// Feature flags to control which data to fetch (reduces API calls)
	FetchSettings     bool // Fetch additional repo settings (default: true)
	FetchCustomProps  bool // Fetch custom properties (default: true)
	FetchBranches     bool // Fetch detailed branch protection (default: true)
	FetchWebhooks     bool // Fetch webhooks (default: true)
	FetchAutolinks    bool // Fetch autolinks (default: true)
	FetchActions      bool // Fetch Actions data (workflows, secrets, variables, runners, cache) (default: true)
	FetchSecurity     bool // Fetch security data (Dependabot, code scanning, secret scanning) (default: true)
	FetchPages        bool // Fetch GitHub Pages config (default: true)
	FetchIssuesData   bool // Fetch issues metadata (default: true)
	FetchPRsData      bool // Fetch pull requests metadata (default: true)
	FetchTraffic      bool // Fetch traffic stats (default: true)
	FetchTags         bool // Fetch detailed tag info (default: true)
	FetchGitRefs      bool // Fetch git references (default: true)
	FetchLFS          bool // Fetch LFS status (default: true)
	FetchFiles        bool // Fetch repository files (default: true)
	FetchContributors bool // Fetch contributors count (default: true)
	FetchCommits      bool // Fetch commit count (default: true)
	FetchIssueEvents  bool // Fetch issue events count (default: true)
}

// RunWithContext orchestrates org/repo processing with context support for cancellation.
//
// This is the main entry point for the stats collection process. It validates inputs,
// loads organization lists, processes each organization sequentially, and handles
// errors according to the FailFast configuration.
//
// Parameters:
//   - ctx: Context for cancellation (typically from signal handling)
//   - config: Configuration including orgs, workers, output file, and feature flags
//
// Returns:
//   - nil on success (all orgs processed, errors collected but not fatal)
//   - validation error if both OrgName and InputFile are empty, or if org name is invalid
//   - context.Canceled error if user interrupts the process (Ctrl-C)
//   - rate limit error if GitHub API rate limit is exhausted before processing
//   - processing error if FailFast is true and any organization fails
//   - file I/O error if output file cannot be written and no fallback succeeds
//
// Error Handling:
//   - If FailFast is false (default), errors are collected and logged but don't stop processing
//   - If FailFast is true, the first error encountered stops all processing
//   - Context cancellation is always treated as an immediate error
//   - Rate limit exhaustion stops processing to prevent API abuse
//
// Side Effects:
//   - Updates global rate limit state
//   - Writes data to output file incrementally (after each org)
//   - Prints progress information to stderr
//   - May automatically resume from existing output file
//
// setupAndValidate performs initial setup and validation for the run.
func setupAndValidate(config *Config) error {
	// Enable debug output if verbose flag is set
	if config.Verbose {
		pterm.EnableDebugMessages()
	}

	// Validate inputs
	if config.OrgName == "" && config.InputFile == "" {
		return fmt.Errorf("must specify either --org or --file")
	}

	if config.OrgName != "" {
		if err := validateOrgName(config.OrgName); err != nil {
			return fmt.Errorf("invalid organization name: %w", err)
		}
	}

	// Set default max workers if not specified
	if config.MaxWorkers <= 0 {
		config.MaxWorkers = 3
	}

	return nil
}

// loadExistingData loads existing repository data for resume functionality.
func loadExistingData(config Config) (map[string]output.RepoStats, error) {
	var existingStats map[string]output.RepoStats
	var err error

	// Always try to read existing data to enable automatic resume
	existingStats, err = output.ReadExistingJSON(config.OutputFile)
	if err != nil {
		if config.Resume {
			// User explicitly requested resume but file is unreadable - this is an error
			pterm.Warning.Printf("Could not read existing data for resume: %v\n", err)
			pterm.Warning.Println("Starting fresh collection (existing file may be corrupted)")
		}
		existingStats = make(map[string]output.RepoStats)
	} else if len(existingStats) > 0 {
		pterm.Info.Printf("Found %d already processed repositories in %s (will skip)\n", len(existingStats), config.OutputFile)
	}

	return existingStats, nil
}

// checkRateLimitWithRetry checks rate limits with retry logic for clock skew issues.
func checkRateLimitWithRetry(config Config) error {
	maxRateLimitRetries := 3
	for attempt := 0; attempt < maxRateLimitRetries; attempt++ {
		// Get current rate limit state before checking
		oldREST := state.Get().GetRateLimit()
		oldGraphQL := state.Get().GetGraphQLRateLimit()

		ghapi.UpdateRateLimitInfo()
		err := state.Get().CheckRateLimit(minAPICallsForOrgProcessing)

		if err == nil {
			return nil // Rate limit check passed
		}

		// Check if this is a "refresh needed" error (after sleep or clock skew)
		if errors.Is(err, state.ErrRateLimitRefreshNeeded) || errors.Is(err, state.ErrClockSkewDetected) {
			if attempt < maxRateLimitRetries-1 {
				// Verify the refresh actually updated the data
				newREST := state.Get().GetRateLimit()
				newGraphQL := state.Get().GetGraphQLRateLimit()

				if oldREST.Remaining == newREST.Remaining &&
					oldGraphQL.Remaining == newGraphQL.Remaining &&
					!oldREST.Reset.IsZero() && !newREST.Reset.IsZero() {
					pterm.Warning.Println("⚠️  Rate limit refresh did not update data, retrying...")
					time.Sleep(2 * time.Second) // Brief pause before retry
				}

				if config.Verbose {
					pterm.Debug.Printf("Rate limit check retry %d/%d after refresh\n", attempt+1, maxRateLimitRetries)
				}
				continue
			}
		}

		// Non-recoverable error or max retries reached
		return err
	}
	return fmt.Errorf("rate limit check failed after %d retries", maxRateLimitRetries)
}

// RunWithContext orchestrates the statistics collection process for organizations.
// processOrganization processes a single organization, including metadata and repositories.
func processOrganization(ctx context.Context, org string, config Config, existingStats map[string]output.RepoStats) error {
	state.Get().PrintOrg(org)

	// Check rate limit
	if err := checkRateLimitWithRetry(config); err != nil {
		return fmt.Errorf("rate limit check failed for org %s: %w", org, err)
	}

	// Fetch org metadata
	orgMeta, err := ghapi.GetEnhancedOrgMetadata(ctx, org, config.Verbose, config.NoPackages)
	if err != nil {
		pterm.Warning.Printf("Failed to fetch org metadata for %s: %v\n", org, err)
		orgMeta = output.OrgMetadata{
			Login: org,
			URL:   fmt.Sprintf("https://github.com/%s", org),
		}
	}

	// Save org metadata
	if err := output.AppendToConsolidatedJSON(config.OutputFile, []output.OrgMetadata{orgMeta}, nil, nil); err != nil {
		pterm.Warning.Printf("Failed to save org metadata for %s: %v\n", org, err)
	} else {
		pterm.Success.Printf("✓ Saved comprehensive org data for %s\n", org)
	}

	// Process repositories
	repoStats, _, _, err := processOrg(ctx, org, config, existingStats, config.OutputFile)
	if err != nil {
		return err
	}

	// Aggregate totals
	aggregateOrgTotals(&orgMeta, repoStats)

	// Update org metadata with totals
	if err := output.AppendToConsolidatedJSON(config.OutputFile, []output.OrgMetadata{orgMeta}, nil, nil); err != nil {
		pterm.Warning.Printf("Failed to update org metadata with aggregated totals for %s: %v\n", org, err)
	} else if config.Verbose {
		pterm.Debug.Printf("Updated org metadata with aggregated totals: %d deploy keys\n", orgMeta.TotalDeployKeysCount)
	}

	pterm.Success.Printf("✓ Completed %s\n", org)
	return nil
}

// RunWithContext orchestrates the statistics collection process for organizations.
func RunWithContext(ctx context.Context, config Config) error {
	if err := setupAndValidate(&config); err != nil {
		return err
	}

	// Set the hostname for GHES support
	state.Get().SetHostname(config.Hostname)

	if config.DryRun {
		return runDryRun(config)
	}

	ghapi.UpdateRateLimitInfo()
	state.Get().PrintRateLimit()
	state.Get().CaptureStartingAPICalls()

	existingStats, err := loadExistingData(config)
	if err != nil {
		return err
	}

	orgs, err := loadOrgs(config.OrgName, config.InputFile)
	if err != nil {
		return err
	}

	var allErrors []error

	for _, org := range orgs {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return fmt.Errorf("operation cancelled by user: %w", ctx.Err())
			}
			return fmt.Errorf("operation cancelled: %w", ctx.Err())
		default:
		}

		if err := processOrganization(ctx, org, config, existingStats); err != nil {
			if config.FailFast {
				return fmt.Errorf("processing organization %s: %w", org, err)
			}
			allErrors = append(allErrors, fmt.Errorf("organization %s: %w", org, err))
		}
	}

	// Report errors
	if len(allErrors) > 0 {
		pterm.Warning.Printf("⚠ %d errors occurred during processing:\n", len(allErrors))
		for _, err := range allErrors {
			pterm.Warning.Printf("  - %s\n", err)
		}
	}

	pterm.Success.Printf("✓ All data saved to %s\n", config.OutputFile)
	ghapi.UpdateRateLimitInfo()
	state.Get().PrintRateLimit()
	state.Get().MarkDone()
	return nil
}

// filterProcessedRepos filters out already processed repositories and returns unprocessed ones
func filterProcessedRepos(allRepoNames []string, org string, existingStats map[string]output.RepoStats, config Config) ([]string, int) {
	unprocessedRepoNames := make([]string, 0, len(allRepoNames))
	skippedCount := 0

	for _, repoName := range allRepoNames {
		key := fmt.Sprintf("%s/%s", org, repoName)
		if _, exists := existingStats[key]; exists {
			skippedCount++
			if config.Verbose {
				pterm.Debug.Printf("Skipping already processed repository: %s/%s\n", org, repoName)
			}
		} else {
			unprocessedRepoNames = append(unprocessedRepoNames, repoName)
		}
	}

	if skippedCount > 0 {
		pterm.Info.Printf("Skipping %d already processed repositories in %s\n", skippedCount, org)
	}

	// Update total repos (only count unprocessed ones)
	state.Get().AddRepos(len(unprocessedRepoNames))

	if config.Verbose {
		pterm.Info.Printf("Found %d repositories in %s (%d new, %d already processed)\n",
			len(allRepoNames), org, len(unprocessedRepoNames), skippedCount)
	}

	return unprocessedRepoNames, skippedCount
}

// loadExistingPackageData loads existing package data (automatically resumes if file exists)
func loadExistingPackageData(org string, config Config, outputFile string) map[string]output.PackageCounts {
	// Always try to load existing package data for automatic resume
	existingPackageCounts, err := output.ReadPackageDataJSON(outputFile)
	if err != nil {
		if config.Resume {
			pterm.Warning.Printf("Failed to read existing package data for %s: %v\n", org, err)
		}
		return nil
	}

	if len(existingPackageCounts) > 0 {
		pterm.Info.Printf("Found existing package data for %s (%d repositories, will reuse)\n", org, len(existingPackageCounts))
	}

	return existingPackageCounts
}

// fetchPackageData fetches package data for the organization and returns both counts and raw package data
func fetchPackageData(ctx context.Context, org string, existingPackageCounts map[string]output.PackageCounts, config Config, outputFile string) (map[string]output.PackageCounts, []output.PackageData) {
	// Skip package fetching if --no-packages flag is set
	if config.NoPackages {
		if config.Verbose {
			pterm.Info.Println("Skipping package data fetch (--no-packages flag set)")
		}
		return make(map[string]output.PackageCounts), []output.PackageData{}
	}

	// Always fetch package data (with resume support if enabled)
	packageProgress, _ := pterm.DefaultProgressbar.WithTotal(6).WithTitle("Fetching packages...").Start()

	packageStart := time.Now()
	packageData, rawPackages, err := ghapi.GetOrgPackageDataWithResume(ctx, org, packageProgress, existingPackageCounts, config.Verbose)
	if err != nil {
		pterm.Warning.Printf("Failed to fetch package data for %s: %v\n", org, err)
		// Continue with empty package data rather than failing completely
		packageData = make(map[string]output.PackageCounts)
		rawPackages = []output.PackageData{}
	}
	if config.Verbose {
		pterm.Debug.Printf("Package data fetched in %v\n", time.Since(packageStart))
	}

	// Save package data immediately to disk
	if len(rawPackages) > 0 {
		// Append packages to consolidated JSON file
		if err := output.AppendToConsolidatedJSON(outputFile, nil, nil, rawPackages); err != nil {
			pterm.Warning.Printf("Failed to save package data for %s: %v\n", org, err)
		} else {
			pterm.Info.Printf("Saved %d packages to %s\n", len(rawPackages), outputFile)
		}
	}

	return packageData, rawPackages
}

// processRepositories processes the repositories and returns statistics and errors
func processRepositories(ctx context.Context, org string, repoNames []string, packageData map[string]output.PackageCounts, teamAccessMap map[string][]output.RepoTeamAccess, config Config, outputFile string) ([]output.RepoStats, []error) {
	if len(repoNames) == 0 {
		pterm.Info.Printf("All repositories in %s have already been processed\n", org)
		return []output.RepoStats{}, []error{}
	}

	// Process each repository individually with a progress bar
	repoProgress, _ := pterm.DefaultProgressbar.
		WithTotal(len(repoNames)).
		WithTitle(fmt.Sprintf("Processing %d repositories...", len(repoNames))).
		WithBarCharacter("█").
		WithLastCharacter("█").
		WithElapsedTimeRoundingFactor(time.Second).
		WithShowElapsedTime(true).
		WithShowCount(true).
		WithShowPercentage(true).
		WithBarStyle(pterm.NewStyle(pterm.FgLightBlue)).
		WithTitleStyle(pterm.NewStyle(pterm.FgLightCyan)).
		Start()

	return processReposParallel(ctx, org, repoNames, packageData, teamAccessMap, repoProgress, config, outputFile)
}

// processOrg processes a single organization and returns statistics, packages, and errors
func processOrg(ctx context.Context, org string, config Config, existingStats map[string]output.RepoStats, outputFile string) ([]output.RepoStats, []output.PackageData, []error, error) {
	// Fetch all repository names for this organization
	allRepoNames, err := ghapi.FetchRepositoryNames(ctx, org)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("fetching repository names: %w", err)
	}

	// Filter out already processed repositories
	repoNames, _ := filterProcessedRepos(allRepoNames, org, existingStats, config)

	// Load existing package data if resume is enabled
	existingPackageCounts := loadExistingPackageData(org, config, outputFile)

	// Fetch package data for the organization (saves immediately)
	packageData, rawPackages := fetchPackageData(ctx, org, existingPackageCounts, config, outputFile)

	// Fetch team access data once for the entire org
	if config.Verbose {
		pterm.Info.Println("Fetching team access data for organization...")
	}
	teamAccessMap, err := ghapi.FetchOrgTeamsAccess(ctx, org, config.Verbose)
	if err != nil {
		pterm.Warning.Printf("Failed to fetch team access data: %v (will fetch per-repo instead)\n", err)
		teamAccessMap = nil // Will fall back to per-repo fetching
	} else if config.Verbose {
		totalTeams := 0
		for _, teams := range teamAccessMap {
			totalTeams += len(teams)
		}
		pterm.Info.Printf("Loaded team access for %d repositories\n", len(teamAccessMap))
	}

	// Debug: Show package data stats
	if config.Verbose {
		pterm.Info.Printf("Package data loaded for %d repositories\n", len(packageData))
		for k, v := range packageData {
			pterm.Debug.Printf("  %s: %d packages, %d versions\n", k, v.PackageCount, v.PackageVersions)
		}
	}

	// Process repositories (with incremental saving)
	stats, errors := processRepositories(ctx, org, repoNames, packageData, teamAccessMap, config, outputFile)

	return stats, rawPackages, errors, nil
}

// aggregateOrgTotals aggregates repository-level metrics into organization-level totals.
func aggregateOrgTotals(orgMeta *output.OrgMetadata, repoStats []output.RepoStats) {
	var totalDeployKeys int

	for _, repo := range repoStats {
		// Aggregate deploy keys count
		totalDeployKeys += len(repo.DeployKeys)
	}

	orgMeta.TotalDeployKeysCount = totalDeployKeys
	// Note: We don't aggregate TotalContributorsCount here because it requires deduplication
	// which would need more sophisticated tracking (same username might be different people).
	// Users can sum Contributors from individual repos if needed.
}

// runDryRun shows what would be collected without making actual API calls.
// This is useful for planning and estimating API usage before running a full collection.
// calculateEstimatedAPICalls estimates API calls per repository based on enabled features.
func calculateEstimatedAPICalls(config Config) int {
	calls := 1 // Base GraphQL query

	flagCounts := map[bool]int{
		!config.NoPackages:       1,
		config.FetchSettings:     1,
		config.FetchCustomProps:  1,
		config.FetchBranches:     1,
		config.FetchWebhooks:     1,
		config.FetchAutolinks:    1,
		config.FetchActions:      5,
		config.FetchSecurity:     7,
		config.FetchPages:        1,
		config.FetchIssuesData:   1,
		config.FetchPRsData:      1,
		config.FetchTraffic:      1,
		config.FetchTags:         1,
		config.FetchGitRefs:      1,
		config.FetchLFS:          1,
		config.FetchFiles:        1,
		config.FetchContributors: 1,
		config.FetchCommits:      1,
		config.FetchIssueEvents:  1,
	}

	for enabled, count := range flagCounts {
		if enabled {
			calls += count
		}
	}

	return calls
}

// printDryRunHeader prints the dry run mode header and organization list.
func printDryRunHeader(orgs []string, config Config) {
	pterm.Info.Println("🔍 DRY RUN MODE - No API calls will be made")
	fmt.Println()

	pterm.Info.Printf("📋 Would process %d organization(s):\n", len(orgs))
	for i, org := range orgs {
		pterm.Info.Printf("  %d. %s\n", i+1, org)
	}
	fmt.Println()

	pterm.Info.Println("⚙️  Configuration:")
	pterm.Info.Printf("  Output file: %s\n", config.OutputFile)
	pterm.Info.Printf("  Max workers: %d\n", config.MaxWorkers)
	pterm.Info.Printf("  Resume mode: %v\n", config.Resume)
	fmt.Println()
}

// printDryRunDataPoints prints what data would be collected.
func printDryRunDataPoints(config Config) {
	pterm.Info.Println("📊 Data to be collected:")

	dataPoints := []struct {
		name    string
		enabled bool
	}{
		{"Repository metadata (GraphQL)", true},
		{"Organization metadata", true},
		{"Package data", !config.NoPackages},
		{"Repository settings", config.FetchSettings},
		{"Custom properties", config.FetchCustomProps},
		{"Branch protection", config.FetchBranches},
		{"Webhooks", config.FetchWebhooks},
		{"Autolinks", config.FetchAutolinks},
		{"GitHub Actions (workflows, secrets, variables, runners, cache)", config.FetchActions},
		{"Security (Dependabot, code scanning, secret scanning)", config.FetchSecurity},
		{"GitHub Pages", config.FetchPages},
		{"Issues metadata", config.FetchIssuesData},
		{"Pull requests metadata", config.FetchPRsData},
		{"Traffic stats", config.FetchTraffic},
		{"Tags", config.FetchTags},
		{"Git references", config.FetchGitRefs},
		{"LFS status", config.FetchLFS},
		{"Repository files", config.FetchFiles},
		{"Contributors count", config.FetchContributors},
		{"Commit count", config.FetchCommits},
		{"Issue events count", config.FetchIssueEvents},
	}

	for _, dp := range dataPoints {
		if dp.enabled {
			pterm.Info.Printf("  ✓ %s\n", dp.name)
		} else {
			grayText := pterm.NewStyle(pterm.FgLightWhite).Sprintf("✗ %s (disabled)", dp.name)
			pterm.Info.Printf("  %s\n", grayText)
		}
	}
	fmt.Println()
}

// runDryRun shows what would be collected without making API calls.
func runDryRun(config Config) error {
	orgs, err := loadOrgs(config.OrgName, config.InputFile)
	if err != nil {
		return fmt.Errorf("failed to load organizations: %w", err)
	}

	printDryRunHeader(orgs, config)
	printDryRunDataPoints(config)

	apiCallsPerRepo := calculateEstimatedAPICalls(config)

	pterm.Warning.Println("⚠️  Estimated API usage:")
	pterm.Warning.Printf("  ~%d API calls per repository\n", apiCallsPerRepo)
	pterm.Warning.Println("  Plus: ~3-5 calls per organization")
	fmt.Println()
	pterm.Warning.Println("  Note: Actual usage may vary based on:")
	pterm.Warning.Println("    - Number of workflows, secrets, branches, etc.")
	pterm.Warning.Println("    - Pagination requirements for large datasets")
	pterm.Warning.Println("    - Rate limit throttling and retries")
	fmt.Println()

	pterm.Success.Println("✓ Dry run complete. Remove --dry-run flag to start actual collection.")
	return nil
}
