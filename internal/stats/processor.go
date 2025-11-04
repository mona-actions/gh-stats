// Package stats implements concurrent processing of GitHub repository statistics.
//
// This file (processor.go) contains the main orchestration logic for the gh stats tool.
// It coordinates the entire data collection process from validation through final output,
// managing multiple organizations, rate limits, and error handling.
//
// Key features:
//   - Multi-organization sequential processing.
//   - Per-organization parallel repository processing.
//   - Automatic resume from interrupted operations.
//   - Dry-run mode for preview and API estimation.
//   - Rate limit checking and reporting.
//   - Comprehensive error aggregation.
//   - Context-aware cancellation support.
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
	OrgName          string // Organization name to analyze (mutually exclusive with InputFile)
	InputFile        string // File with list of organizations to analyze (mutually exclusive with OrgName)
	OutputFile       string // Output file path for results (JSON format)
	Version          string // Version string for display in banner (set by main package)
	MaxWorkers       int    // Maximum number of concurrent API calls (default: 3)
	GraphQLBatchSize int    // Number of repos to fetch per GraphQL query (default: 20, max: 50)
	FailFast         bool   // Stop processing on first error
	Verbose          bool   // Enable verbose output and debug information
	Resume           bool   // Resume from existing output file, skipping already processed repositories
	DryRun           bool   // Show what would be collected without making API calls (preview mode)
	NoPackages       bool   // Skip fetching package data for faster execution
	NoTeams          bool   // Skip fetching team access data (significant API savings for large orgs)

	// Embed DataFetchConfig to avoid field duplication
	ghapi.DataFetchConfig
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
		// File doesn't exist or is corrupted - start fresh
		if config.Verbose {
			pterm.Debug.Printf("No existing data to resume from: %v\n", err)
		}
		existingStats = make(map[string]output.RepoStats)
	} else if len(existingStats) > 0 {
		pterm.Info.Printf("Found %d repositories in %s (will skip)\n", len(existingStats), config.OutputFile)
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

// printBanner displays the gh-stats startup banner with version information.
// The banner uses ASCII art and pterm styling for a professional appearance.
func printBanner(version string) {
	if version == "" {
		version = "dev"
	}

	banner := fmt.Sprintf(`
    ██████╗ ██╗  ██╗    ███████╗████████╗ █████╗ ████████╗███████╗   
   ██╔════╝ ██║  ██║    ██╔════╝╚══██╔══╝██╔══██╗╚══██╔══╝██╔════╝   
   ██║  ███╗███████║    ███████╗   ██║   ███████║   ██║   ███████╗   
   ██║   ██║██╔══██║    ╚════██║   ██║   ██╔══██║   ██║   ╚════██║   
   ╚██████╔╝██║  ██║    ███████║   ██║   ██║  ██║   ██║   ███████║   
    ╚═════╝ ╚═╝  ╚═╝    ╚══════╝   ╚═╝   ╚═╝  ╚═╝   ╚═╝   ╚══════╝   
   📦 GitHub Stats Collector • %s
`, version)

	pterm.DefaultBox.WithBoxStyle(pterm.NewStyle(pterm.FgCyan)).
		WithHorizontalString("═").
		WithVerticalString("║").
		Println(banner)
	fmt.Println()
}

// RunWithContext orchestrates the statistics collection process for organizations.
// processOrganization processes a single organization, including metadata and repositories.
func processOrganization(ctx context.Context, org string, config Config, existingStats map[string]output.RepoStats) error {
	// Print organization header
	output.PrintOrgHeader(org)

	// Check rate limit
	if err := checkRateLimitWithRetry(config); err != nil {
		return fmt.Errorf("rate limit check failed for org %s: %w", org, err)
	}

	// Fetch org metadata with spinner
	spinner, _ := pterm.DefaultSpinner.WithRemoveWhenDone(true).Start("Fetching organization metadata...")
	orgMeta, err := ghapi.GetEnhancedOrgMetadata(ctx, org, config.Verbose, config.NoPackages)
	_ = spinner.Stop()

	if err != nil {
		pterm.Warning.Printf("Failed to fetch org metadata for %s: %v\n", org, err)
		orgMeta = output.OrgMetadata{
			Login: org,
			URL:   fmt.Sprintf("https://github.com/%s", org),
		}
	}

	// Print org metadata summary (non-verbose mode)
	if !config.Verbose {
		output.PrintOrgMetadata(output.OrgMetadataDisplay{
			SecurityManagers:  len(orgMeta.SecurityManagers),
			CustomProperties:  len(orgMeta.CustomProperties),
			Members:           orgMeta.MembersCount,
			OutsideCollabs:    orgMeta.OutsideCollaboratorsCount,
			Teams:             orgMeta.TeamsCount,
			Secrets:           len(orgMeta.ActionsSecrets),
			Variables:         len(orgMeta.ActionsVariables),
			Rulesets:          len(orgMeta.Rulesets),
			SelfHostedRunners: orgMeta.RunnersCount,
			BlockedUsers:      len(orgMeta.BlockedUsers),
			Webhooks:          len(orgMeta.Webhooks),
			SkippedPackages:   config.NoPackages,
		})
	} else {
		pterm.Success.Println("✅ Organization metadata saved")
		pterm.Println()
	}

	// Save org metadata
	if err := output.AppendToConsolidatedJSON(config.OutputFile, []output.OrgMetadata{orgMeta}, nil, nil); err != nil {
		pterm.Warning.Printf("Failed to save org metadata for %s: %v\n", org, err)
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

	// Completion message removed - shown in MarkDoneWithSummary() instead
	return nil
}

// RunWithContext orchestrates the statistics collection process for organizations.
func RunWithContext(ctx context.Context, config Config) error {
	// Display startup banner
	printBanner(config.Version)

	if err := setupAndValidate(&config); err != nil {
		return err
	}

	if config.DryRun {
		return runDryRun(config)
	}

	// Track start time for duration calculation
	startTime := time.Now()

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

	// Add spacing before summary
	pterm.Println()
	ghapi.UpdateRateLimitInfo()
	state.Get().PrintRateLimit()

	// Calculate duration and mode
	duration := time.Since(startTime)
	mode := getModeName(config.DataFetchConfig)

	state.Get().MarkDoneWithSummary(len(orgs), config.OutputFile, duration, mode, len(allErrors))
	return nil
}

// getModeName returns a human-readable name for the data fetch mode based on config.
func getModeName(fetchConfig ghapi.DataFetchConfig) string {
	// Count enabled features using a slice
	features := []bool{
		fetchConfig.FetchSettings,
		fetchConfig.FetchCustomProps,
		fetchConfig.FetchBranches,
		fetchConfig.FetchWebhooks,
		fetchConfig.FetchAutolinks,
		fetchConfig.FetchActions,
		fetchConfig.FetchSecurity,
		fetchConfig.FetchPages,
		fetchConfig.FetchIssuesData,
		fetchConfig.FetchPRsData,
		fetchConfig.FetchTraffic,
		fetchConfig.FetchTags,
		fetchConfig.FetchGitRefs,
		fetchConfig.FetchLFS,
		fetchConfig.FetchFiles,
		fetchConfig.FetchContributors,
		fetchConfig.FetchCommits,
		fetchConfig.FetchIssueEvents,
		fetchConfig.FetchCollaborators,
		fetchConfig.FetchLanguages,
		fetchConfig.FetchTopics,
		fetchConfig.FetchLicense,
		fetchConfig.FetchDeployKeys,
		fetchConfig.FetchEnvironments,
		fetchConfig.FetchDeployments,
	}

	enabledCount := 0
	for _, enabled := range features {
		if enabled {
			enabledCount++
		}
	}

	totalFeatures := len(features)

	// Determine mode based on enabled count
	if enabledCount == 0 {
		return "Minimal"
	} else if enabledCount < totalFeatures/3 {
		return "Minimal"
	} else if enabledCount < (totalFeatures*2)/3 {
		return "Partial"
	}
	return "Full"
}

// filterProcessedRepos filters out already processed repositories and returns unprocessed ones.
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

	// Print repository discovery information
	output.PrintRepoDiscovery(output.RepoDiscovery{
		Total:           len(allRepoNames),
		New:             len(unprocessedRepoNames),
		AlreadyDone:     skippedCount,
		SkippedPackages: config.NoPackages,
		SkippedTeams:    config.NoTeams,
	})

	return unprocessedRepoNames, skippedCount
}

// loadExistingPackageData loads existing package data (automatically resumes if file exists).
func loadExistingPackageData(org string, config Config, outputFile string) map[string]output.PackageCounts {
	// Always try to load existing package data for automatic resume
	existingPackageCounts, err := output.ReadPackageDataJSON(outputFile)
	if err != nil {
		if config.Verbose {
			pterm.Debug.Printf("No existing package data for %s: %v\n", org, err)
		}
		return nil
	}

	if len(existingPackageCounts) > 0 && config.Verbose {
		pterm.Info.Printf("📦 Resuming: Found package data for %d repositories\n", len(existingPackageCounts))
	}

	return existingPackageCounts
}

// fetchPackageData fetches package data for the organization and returns both counts and raw package data.
func fetchPackageData(ctx context.Context, org string, existingPackageCounts map[string]output.PackageCounts, config Config, outputFile string) (map[string]output.PackageCounts, []output.PackageData) {
	// Skip package fetching if --no-packages flag is set
	if config.NoPackages {
		if config.Verbose {
			pterm.Info.Println("Skipping package data fetch (--no-packages flag set)")
		}
		return make(map[string]output.PackageCounts), []output.PackageData{}
	}

	// Always fetch package data (with automatic resume if existing data found)
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
			// Calculate package type breakdown and total versions
			typeBreakdown := make(map[string]int)
			totalVersions := 0

			for _, pkg := range rawPackages {
				typeBreakdown[pkg.PackageType]++
				if pkg.VersionCount != nil {
					totalVersions += *pkg.VersionCount
				}
			}

			// Add spacing before summary
			pterm.Println()

			// Fancy package summary with emoji (INFO for less visual weight)
			pterm.Info.Printf("📦 Package Summary\n")
			pterm.Info.Printf("   ├─ Total: %d packages (%d versions)\n", len(rawPackages), totalVersions)

			// Show breakdown by type
			typeCount := 0
			for pkgType, count := range typeBreakdown {
				typeCount++
				if typeCount == len(typeBreakdown) {
					pterm.Info.Printf("   └─ %s: %d\n", pkgType, count)
				} else {
					pterm.Info.Printf("   ├─ %s: %d\n", pkgType, count)
				}
			}

			// Now print completion message
			pterm.Println()
			pterm.Success.Println("✅ Package fetching complete")
		}
	}

	return packageData, rawPackages
}

// processRepositories processes the repositories and returns statistics and errors.
func processRepositories(ctx context.Context, org string, repoNames []string, packageData map[string]output.PackageCounts, teamAccessMap map[string][]output.RepoTeamAccess, config Config, outputFile string) ([]output.RepoStats, []error) {
	if len(repoNames) == 0 {
		pterm.Println()
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

// processOrg processes a single organization and returns statistics, packages, and errors.
// This is the core implementation that orchestrates fetching packages, team access, and repository data.
func processOrg(ctx context.Context, org string, config Config, existingStats map[string]output.RepoStats, outputFile string) ([]output.RepoStats, []output.PackageData, []error, error) {
	// Fetch all repository names for this organization
	allRepoNames, err := ghapi.FetchRepositoryNames(ctx, org)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("fetching repository names: %w", err)
	}

	// Filter out already processed repositories
	repoNames, _ := filterProcessedRepos(allRepoNames, org, existingStats, config)

	// Load existing package data if file exists (automatic resume)
	existingPackageCounts := loadExistingPackageData(org, config, outputFile)

	// Fetch package data for the organization (saves immediately)
	packageData, rawPackages := fetchPackageData(ctx, org, existingPackageCounts, config, outputFile)

	// Fetch team access data once for the entire org (unless disabled)
	var teamAccessMap map[string][]output.RepoTeamAccess
	if config.NoTeams {
		if config.Verbose {
			pterm.Info.Println("Skipping team access data fetch (--no-teams flag set)")
		}
		teamAccessMap = nil
	} else {
		if config.Verbose {
			pterm.Info.Println("Fetching team access data for organization...")
		}
		var err error
		teamAccessMap, err = ghapi.FetchOrgTeamsAccess(ctx, org, config.Verbose)
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
	pterm.Println()

	pterm.Info.Printf("📋 Would process %d organization(s):\n", len(orgs))
	for i, org := range orgs {
		pterm.Info.Printf("  %d. %s\n", i+1, org)
	}
	pterm.Println()

	pterm.Info.Println("⚙️  Configuration:")
	pterm.Info.Printf("  Output file: %s\n", config.OutputFile)
	pterm.Info.Printf("  Max workers: %d\n", config.MaxWorkers)
	pterm.Println()
}

// printDryRunDataPoints prints what data would be collected.
func printDryRunDataPoints(config Config) {
	pterm.Info.Println("📊 Data to be collected:")
	pterm.Println()

	// Organization-level data
	pterm.Info.Println("  Organization metadata:")
	pterm.Info.Println("    ✓ Basic organization info")
	printFeatureStatus("Package data", !config.NoPackages)
	pterm.Println()

	// GraphQL data (single query per repo)
	pterm.Info.Println("  Repository data (GraphQL):")
	pterm.Info.Println("    ✓ Base fields (metadata, counts, feature flags)")

	graphqlFeatures := []struct {
		name    string
		enabled bool
	}{
		{"Collaborators list", config.FetchCollaborators},
		{"Language breakdown", config.FetchLanguages},
		{"Topics", config.FetchTopics},
		{"License info", config.FetchLicense},
		{"Deploy keys", config.FetchDeployKeys},
		{"Environments", config.FetchEnvironments},
		{"Deployments", config.FetchDeployments},
		{"Milestones", config.FetchMilestones},
		{"Releases", config.FetchReleases},
		{"Community files", config.FetchCommunityFiles},
		{"Rulesets", config.FetchRulesets},
		{"Branch protection rules", config.FetchBranchProtection},
	}
	printFeatureList(graphqlFeatures)
	pterm.Println()

	// REST API data (multiple calls per repo)
	pterm.Info.Println("  Repository data (REST API):")
	restFeatures := []struct {
		name    string
		enabled bool
	}{
		{"Repository settings", config.FetchSettings},
		{"Custom properties", config.FetchCustomProps},
		{"Branch details", config.FetchBranches},
		{"Webhooks", config.FetchWebhooks},
		{"Autolinks", config.FetchAutolinks},
		{"GitHub Actions", config.FetchActions},
		{"Security scanning", config.FetchSecurity},
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
	printFeatureList(restFeatures)
	pterm.Println()
}

// printFeatureStatus prints a single feature status line with appropriate styling.
func printFeatureStatus(name string, enabled bool) {
	if enabled {
		pterm.Info.Printf("    ✓ %s\n", name)
	} else {
		grayText := pterm.NewStyle(pterm.FgLightWhite).Sprintf("✗ %s (disabled)", name)
		pterm.Info.Printf("    %s\n", grayText)
	}
}

// printFeatureList prints a list of features with their status.
func printFeatureList(features []struct {
	name    string
	enabled bool
}) {
	for _, feature := range features {
		printFeatureStatus(feature.name, feature.enabled)
	}
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
	pterm.Println()
	pterm.Warning.Println("  Note: Actual usage may vary based on:")
	pterm.Warning.Println("    - Number of workflows, secrets, branches, etc.")
	pterm.Warning.Println("    - Pagination requirements for large datasets")
	pterm.Warning.Println("    - Rate limit throttling and retries")
	pterm.Println()

	pterm.Success.Println("✓ Dry run complete. Remove --dry-run flag to start actual collection.")
	return nil
}
