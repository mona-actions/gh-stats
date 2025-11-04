// Package ghapi provides GitHub API client functionality.
//
// This file (graphql_fetch.go) contains high-level repository data fetching functions.
// It provides functions to fetch complete repository stats using GraphQL and REST APIs.
package ghapi

import (
	"context"
	"fmt"
	"time"

	"github.com/mona-actions/gh-stats/internal/output"
)

// GetRepoDetails fetches comprehensive repository statistics and metadata using GraphQL.
//
// This function retrieves all repository data in a single GraphQL query, including base
// information, settings, languages, topics, and optionally expensive fields like contributors,
// issues, and pull requests based on fetchConfig.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - org: Organization name
//   - repoName: Repository name
//   - packageData: Package counts for this repository (may be nil)
//   - teamAccessMap: Team access information for this repository (may be nil)
//   - fetchConfig: Configuration specifying which data fields to fetch
//
// Returns comprehensive RepoStats or an error if the query fails.
func GetRepoDetails(ctx context.Context, org, repoName string, packageData map[string]output.PackageCounts, teamAccessMap map[string][]output.RepoTeamAccess, fetchConfig DataFetchConfig) (output.RepoStats, error) {
	// Add per-operation timeout to prevent indefinite hangs on unresponsive API
	// This protects against: network issues, API bugs, extremely slow responses
	opCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// Build query dynamically based on fetchConfig to control complexity
	query := buildRepositoryQuery(fetchConfig)

	vars := map[string]interface{}{
		"login":    org,
		"repoName": repoName,
	}

	// Execute the GraphQL query with per-operation timeout
	result, err := RunGraphQLPaginated(opCtx, query, vars, func(data map[string]interface{}) (string, bool) {
		return "", false // Single page query - no pagination needed
	})
	if err != nil {
		return output.RepoStats{}, fmt.Errorf("GraphQL query failed: %w", err)
	}

	// Get issue events count using REST API (optional based on fetchConfig)
	var issueEventsCount int
	if fetchConfig.FetchIssueEvents {
		count, err := GetIssueEventsCount(opCtx, org, repoName)
		if err != nil {
			// Log error but continue with 0 count
			issueEventsCount = 0
		} else {
			issueEventsCount = count
		}
	}

	// Extract repository data from the first (and only) page
	if len(result) == 0 {
		return output.RepoStats{}, fmt.Errorf("no data returned for repository %s", repoName)
	}

	repoData := result[0]

	// GraphQL responses wrap data in a "data" key
	if dataWrapper, ok := repoData["data"].(map[string]interface{}); ok {
		repoData = dataWrapper
	}

	orgData, ok := repoData["organization"].(map[string]interface{})
	if !ok {
		return output.RepoStats{}, fmt.Errorf("invalid organization data structure")
	}

	repo, ok := orgData["repository"].(map[string]interface{})
	if !ok {
		return output.RepoStats{}, fmt.Errorf("repository %s not found", repoName)
	}

	// Build the basic repository statistics
	basicStats := buildBasicRepoStats(org, repo, packageData, issueEventsCount)

	// Fetch enhanced repository data (all phases)
	enhancedData, err := GetEnhancedRepoData(opCtx, org, repoName, teamAccessMap, fetchConfig)
	if err != nil {
		// Log warning but continue with basic stats
		fmt.Printf(" WARNING Failed to fetch enhanced data for %s/%s: %v\n", org, repoName, err)
		return basicStats, nil
	}

	// Merge basic stats with enhanced data
	mergeEnhancedData(&basicStats, enhancedData)

	return basicStats, nil
}

// GetBatchedRepoBasicDetails fetches ONLY base GraphQL data for multiple repositories in a single query.
// This is step 1 of the batching strategy - get cheap fields for many repos at once.
// REST API calls and expensive GraphQL fields should be fetched per-repo after this returns.
//
// Returns:
//   - Map of repo name -> basic repo data (raw GraphQL response)
//   - List of repos that failed or weren't found in the batch
//   - Error if the entire batch query failed
func GetBatchedRepoBasicDetails(ctx context.Context, org string, repoNames []string, fetchConfig DataFetchConfig) (map[string]map[string]interface{}, []string, error) {
	if len(repoNames) == 0 {
		return make(map[string]map[string]interface{}), nil, nil
	}

	// Use base-only config for batching to keep queries small and fast
	baseConfig := getBaseOnlyConfig(fetchConfig)

	// Build the batched query with only cheap fields
	query := buildBatchedRepositoryQuery(repoNames, baseConfig)

	// Variables for the query
	vars := map[string]interface{}{
		"login": org,
	}

	// Execute the batched query with reasonable timeout (60 seconds)
	opCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	result, err := RunGraphQLPaginated(opCtx, query, vars, func(data map[string]interface{}) (string, bool) {
		// Note: API call counter incremented by executeGraphQLPage, not here
		return "", false // Batched queries don't paginate
	})
	if err != nil {
		return nil, repoNames, fmt.Errorf("batched GraphQL query failed: %w", err)
	}

	if len(result) == 0 {
		return nil, repoNames, fmt.Errorf("no data returned for batched query")
	}

	// Parse the response
	repoData := result[0]
	if dataWrapper, ok := repoData["data"].(map[string]interface{}); ok {
		repoData = dataWrapper
	}

	orgData, ok := repoData["organization"].(map[string]interface{})
	if !ok {
		return nil, repoNames, fmt.Errorf("invalid organization data structure")
	}

	// Extract each repository from the aliased results
	repoDataMap := make(map[string]map[string]interface{})
	failedRepos := make([]string, 0)

	for i, repoName := range repoNames {
		alias := fmt.Sprintf("repo_%d", i)

		repo, ok := orgData[alias].(map[string]interface{})
		if !ok || repo == nil {
			// Repository not found or error - add to failed list
			failedRepos = append(failedRepos, repoName)
			continue
		}

		repoDataMap[repoName] = repo
	}

	return repoDataMap, failedRepos, nil
}

// ProcessRepoWithBasicData takes basic GraphQL data and enriches it with REST API and expensive GraphQL fields.
// This is step 2 - called per-repo after batch GraphQL completes.
// This allows each repo to be processed independently with immediate progress reporting.
func ProcessRepoWithBasicData(ctx context.Context, org, repoName string, basicRepoData map[string]interface{}, packageData map[string]output.PackageCounts, teamAccessMap map[string][]output.RepoTeamAccess, fetchConfig DataFetchConfig) (output.RepoStats, error) {
	// Get issue events count if enabled (REST API call with short timeout)
	var issueEventsCount int
	if fetchConfig.FetchIssueEvents {
		restCtx, restCancel := context.WithTimeout(ctx, 30*time.Second)
		count, err := GetIssueEventsCount(restCtx, org, repoName)
		restCancel()
		if err != nil {
			issueEventsCount = 0
		} else {
			issueEventsCount = count
		}
	}

	// Build basic stats from GraphQL data
	basicStats := buildBasicRepoStats(org, basicRepoData, packageData, issueEventsCount)

	// Fetch enhanced repository data (REST API calls) with timeout
	enhancedCtx, enhancedCancel := context.WithTimeout(ctx, 60*time.Second)
	enhancedData, err := GetEnhancedRepoData(enhancedCtx, org, repoName, teamAccessMap, fetchConfig)
	enhancedCancel()
	if err != nil {
		// Log warning but continue with basic stats
		if fetchConfig.FetchSettings || fetchConfig.FetchBranches || fetchConfig.FetchWebhooks {
			fmt.Printf(" WARNING Failed to fetch enhanced data for %s/%s: %v\n", org, repoName, err)
		}
	} else {
		// Merge basic stats with enhanced data
		mergeEnhancedData(&basicStats, enhancedData)
	}

	// Fetch expensive GraphQL fields separately if any are enabled
	if hasExpensiveFields(fetchConfig) {
		expensiveCtx, expensiveCancel := context.WithTimeout(ctx, 30*time.Second)
		expensiveConfig := getExpensiveOnlyConfig(fetchConfig)
		expensiveData, err := fetchExpensiveGraphQLFields(expensiveCtx, org, repoName, expensiveConfig)
		expensiveCancel()
		if err != nil {
			fmt.Printf(" WARNING Failed to fetch expensive fields for %s/%s: %v\n", org, repoName, err)
		} else {
			// Merge expensive fields into the stats
			mergeExpensiveFields(&basicStats, expensiveData)
		}
	}

	return basicStats, nil
}

// expensiveFieldExtractor defines a function that extracts a specific expensive field from repo data.
type expensiveFieldExtractor func(repo map[string]interface{}, stats *output.RepoStats)

// expensiveFieldMapping maps config flags to their extraction functions.
var expensiveFieldMapping = []struct {
	enabled   func(DataFetchConfig) bool
	extractor expensiveFieldExtractor
}{
	{
		enabled: func(cfg DataFetchConfig) bool { return cfg.FetchCollaborators },
		extractor: func(repo map[string]interface{}, stats *output.RepoStats) {
			stats.CollaboratorsDetailed = extractCollaborators(repo)
		},
	},
	{
		enabled: func(cfg DataFetchConfig) bool { return cfg.FetchRulesets },
		extractor: func(repo map[string]interface{}, stats *output.RepoStats) {
			stats.Rulesets = extractRulesets(repo)
		},
	},
	{
		enabled: func(cfg DataFetchConfig) bool { return cfg.FetchMilestones },
		extractor: func(repo map[string]interface{}, stats *output.RepoStats) {
			milestones := extractMilestones(repo)
			if len(milestones) > 0 {
				if stats.IssuesData == nil {
					stats.IssuesData = &output.IssuesData{}
				}
				stats.IssuesData.Milestones = milestones
				stats.IssuesData.MilestonesCount = len(milestones)
			}
		},
	},
	{
		enabled: func(cfg DataFetchConfig) bool { return cfg.FetchLanguages },
		extractor: func(repo map[string]interface{}, stats *output.RepoStats) {
			stats.Languages = extractLanguages(repo)
		},
	},
	{
		enabled: func(cfg DataFetchConfig) bool { return cfg.FetchTopics },
		extractor: func(repo map[string]interface{}, stats *output.RepoStats) {
			stats.Topics = extractTopics(repo)
		},
	},
	{
		enabled: func(cfg DataFetchConfig) bool { return cfg.FetchLicense },
		extractor: func(repo map[string]interface{}, stats *output.RepoStats) {
			stats.License = extractLicense(repo)
		},
	},
	{
		enabled: func(cfg DataFetchConfig) bool { return cfg.FetchDeployKeys },
		extractor: func(repo map[string]interface{}, stats *output.RepoStats) {
			stats.DeployKeys = extractDeployKeys(repo)
		},
	},
	{
		enabled: func(cfg DataFetchConfig) bool { return cfg.FetchEnvironments },
		extractor: func(repo map[string]interface{}, stats *output.RepoStats) {
			stats.Environments = extractEnvironments(repo)
		},
	},
	{
		enabled: func(cfg DataFetchConfig) bool { return cfg.FetchDeployments },
		extractor: func(repo map[string]interface{}, stats *output.RepoStats) {
			stats.Deployments = extractDeployments(repo)
		},
	},
	{
		enabled: func(cfg DataFetchConfig) bool { return cfg.FetchCommunityFiles },
		extractor: func(repo map[string]interface{}, stats *output.RepoStats) {
			if communityProfile := extractCommunityProfile(repo); communityProfile != nil {
				stats.CommunityProfile = communityProfile
			}
		},
	},
}

// fetchExpensiveGraphQLFields fetches expensive GraphQL fields for a single repository.
// This is called per-repo after batching to avoid overwhelming the batch query.
func fetchExpensiveGraphQLFields(ctx context.Context, org, repoName string, expensiveConfig DataFetchConfig) (*output.RepoStats, error) {
	// Build query with only expensive fields
	query := buildRepositoryQuery(expensiveConfig)

	vars := map[string]interface{}{
		"login":    org,
		"repoName": repoName,
	}

	// Execute the query
	result, err := RunGraphQLPaginated(ctx, query, vars, func(data map[string]interface{}) (string, bool) {
		// Note: API call counter incremented by executeGraphQLPage, not here
		return "", false // Single page query
	})
	if err != nil {
		return nil, fmt.Errorf("expensive fields GraphQL query failed: %w", err)
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no data returned for expensive fields query")
	}

	// Parse the response
	repo, err := extractRepoFromResponse(result[0])
	if err != nil {
		return nil, err
	}

	// Build stats from expensive fields only
	stats := &output.RepoStats{
		Org:  org,
		Name: repoName,
	}

	// Extract all enabled expensive fields using the mapping table
	extractExpensiveFields(repo, stats, expensiveConfig)

	return stats, nil
}

// extractRepoFromResponse extracts the repository object from a GraphQL response.
func extractRepoFromResponse(result map[string]interface{}) (map[string]interface{}, error) {
	repoData := result
	if dataWrapper, ok := repoData["data"].(map[string]interface{}); ok {
		repoData = dataWrapper
	}

	orgData, ok := repoData["organization"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid organization data structure")
	}

	repo, ok := orgData["repository"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("repository not found")
	}

	return repo, nil
}

// extractExpensiveFields extracts all enabled expensive fields from repo data.
func extractExpensiveFields(repo map[string]interface{}, stats *output.RepoStats, config DataFetchConfig) {
	for _, mapping := range expensiveFieldMapping {
		if mapping.enabled(config) {
			mapping.extractor(repo, stats)
		}
	}
}

// mergeExpensiveFields merges expensive GraphQL fields into the basic stats.
func mergeExpensiveFields(basicStats *output.RepoStats, expensiveData *output.RepoStats) {
	if len(expensiveData.CollaboratorsDetailed) > 0 {
		basicStats.CollaboratorsDetailed = expensiveData.CollaboratorsDetailed
	}

	if len(expensiveData.Rulesets) > 0 {
		basicStats.Rulesets = expensiveData.Rulesets
	}

	// Merge milestones into IssuesData
	if expensiveData.IssuesData != nil && len(expensiveData.IssuesData.Milestones) > 0 {
		if basicStats.IssuesData == nil {
			basicStats.IssuesData = &output.IssuesData{}
		}
		basicStats.IssuesData.Milestones = expensiveData.IssuesData.Milestones
		basicStats.IssuesData.MilestonesCount = expensiveData.IssuesData.MilestonesCount
	}

	if len(expensiveData.Languages) > 0 {
		basicStats.Languages = expensiveData.Languages
	}

	if len(expensiveData.Topics) > 0 {
		basicStats.Topics = expensiveData.Topics
	}

	if expensiveData.License != nil {
		basicStats.License = expensiveData.License
	}

	if len(expensiveData.DeployKeys) > 0 {
		basicStats.DeployKeys = expensiveData.DeployKeys
	}

	if len(expensiveData.Environments) > 0 {
		basicStats.Environments = expensiveData.Environments
	}

	if len(expensiveData.Deployments) > 0 {
		basicStats.Deployments = expensiveData.Deployments
	}

	if expensiveData.CommunityProfile != nil {
		basicStats.CommunityProfile = expensiveData.CommunityProfile
	}
}

// mergeEnhancedData merges enhanced REST API data with GraphQL-based basic stats.
func mergeEnhancedData(basicStats *output.RepoStats, enhancedData *output.RepoStats) {
	// Settings: merge with GraphQL data
	mergeSettings(basicStats, enhancedData)

	// Use GraphQL data if available, otherwise REST
	if len(basicStats.CollaboratorsDetailed) == 0 {
		basicStats.CollaboratorsDetailed = enhancedData.CollaboratorsDetailed
	}

	basicStats.TeamAccess = enhancedData.TeamAccess

	if len(basicStats.Rulesets) == 0 {
		basicStats.Rulesets = enhancedData.Rulesets
	}

	basicStats.Webhooks = enhancedData.Webhooks

	if len(basicStats.DeployKeys) == 0 {
		basicStats.DeployKeys = enhancedData.DeployKeys
	}

	basicStats.Autolinks = enhancedData.Autolinks
	basicStats.Actions = enhancedData.Actions

	if len(basicStats.Environments) == 0 {
		basicStats.Environments = enhancedData.Environments
	}

	// Security: Merge with GraphQL data
	mergeSecurity(basicStats, enhancedData)

	if len(basicStats.Topics) == 0 {
		basicStats.Topics = enhancedData.Topics
	}

	if len(basicStats.Languages) == 0 {
		basicStats.Languages = enhancedData.Languages
	}

	if basicStats.License == nil {
		basicStats.License = enhancedData.License
	}

	basicStats.Pages = enhancedData.Pages
	basicStats.CustomPropertiesRepo = enhancedData.CustomPropertiesRepo

	// Phase 3 data: Merge issues and PRs
	mergeIssuesData(basicStats, enhancedData)
	basicStats.PullRequestsData = enhancedData.PullRequestsData

	// Phase 4 data
	basicStats.Traffic = enhancedData.Traffic

	if basicStats.CommunityProfile == nil {
		basicStats.CommunityProfile = enhancedData.CommunityProfile
	}

	// Phase 5 data
	if len(basicStats.Deployments) == 0 {
		basicStats.Deployments = enhancedData.Deployments
	}

	basicStats.GitReferencesCount = enhancedData.GitReferencesCount
	basicStats.GitLFSEnabled = enhancedData.GitLFSEnabled
	basicStats.RepoFiles = enhancedData.RepoFiles

	// Phase 6: Engagement metrics
	basicStats.Contributors = enhancedData.Contributors
	basicStats.Commits = enhancedData.Commits
}

// mergeSettings merges REST settings with GraphQL settings.
func mergeSettings(basicStats *output.RepoStats, enhancedData *output.RepoStats) {
	if enhancedData.Settings == nil {
		return
	}

	if basicStats.Settings == nil {
		basicStats.Settings = enhancedData.Settings
	} else {
		// Copy additional fields from REST that aren't in GraphQL
		basicStats.Settings.DefaultBranch = enhancedData.Settings.DefaultBranch
	}
}

// mergeSecurity merges REST security data with GraphQL security data.
func mergeSecurity(basicStats *output.RepoStats, enhancedData *output.RepoStats) {
	if basicStats.Security == nil {
		basicStats.Security = enhancedData.Security
		return
	}

	if enhancedData.Security == nil {
		return
	}

	// Merge nested structs
	if enhancedData.Security.Dependabot != nil {
		basicStats.Security.Dependabot = enhancedData.Security.Dependabot
	}
	if enhancedData.Security.CodeScanning != nil {
		basicStats.Security.CodeScanning = enhancedData.Security.CodeScanning
	}
	if enhancedData.Security.SecretScanning != nil {
		basicStats.Security.SecretScanning = enhancedData.Security.SecretScanning
	}
	if enhancedData.Security.SecurityAdvisories != nil {
		basicStats.Security.SecurityAdvisories = enhancedData.Security.SecurityAdvisories
	}
}

// mergeIssuesData merges REST issues data with GraphQL issues data.
func mergeIssuesData(basicStats *output.RepoStats, enhancedData *output.RepoStats) {
	if basicStats.IssuesData == nil {
		basicStats.IssuesData = enhancedData.IssuesData
		return
	}

	if enhancedData.IssuesData == nil {
		return
	}

	// Merge: GraphQL has labels count and milestones, REST has other details
	basicStats.IssuesData.OpenCount = enhancedData.IssuesData.OpenCount
	basicStats.IssuesData.ClosedCount = enhancedData.IssuesData.ClosedCount
	basicStats.IssuesData.TotalCount = enhancedData.IssuesData.TotalCount

	// Keep GraphQL milestones data (already populated)
	if len(basicStats.IssuesData.Milestones) == 0 {
		basicStats.IssuesData.MilestonesCount = enhancedData.IssuesData.MilestonesCount
		basicStats.IssuesData.Milestones = enhancedData.IssuesData.Milestones
	}
}

// buildBasicRepoStats builds the RepoStats struct from the repository data and package counts.
