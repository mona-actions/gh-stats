// Package ghapi provides GitHub API client functionality.
//
// This file (graphql.go) contains GraphQL query operations for bulk data fetching.
// It provides efficient functions to fetch repository lists and comprehensive repository
// data using GitHub's GraphQL API.
//
// Key features:
//   - GraphQL-based repository enumeration
//   - Comprehensive repository metadata fetching
//   - Pagination handling for large organizations
//   - Team access pre-fetching optimization
//   - Efficient bulk data collection
//   - Per-operation timeouts (5 minutes) to prevent hangs
package ghapi

import (
	"context"
	"fmt"
	"time"

	"github.com/mona-actions/gh-stats/internal/output"
	"github.com/mona-actions/gh-stats/internal/state"
)

// FetchRepositoryNames fetches all repository names for an organization using GraphQL pagination.
func FetchRepositoryNames(ctx context.Context, org string) ([]string, error) {
	repoNamesQuery := `
	query($login: String!, $pageSize: Int!, $endCursor: String) {
		organization(login: $login) {
			repositories(first: $pageSize, after: $endCursor, orderBy: {field: NAME, direction: ASC}) {
				pageInfo {
					endCursor
					hasNextPage
				}
				nodes {
					name
				}
			}
		}
	}`

	vars := map[string]interface{}{
		"login":    org,
		"pageSize": 100, // Get more repos per page since we only need names
	}

	// Get all repository names
	pages, err := RunGraphQLPaginated(ctx, repoNamesQuery, vars, func(data map[string]interface{}) (string, bool) {
		state.Get().IncrementAPICalls()
		return extractRepoPagination(data)
	})
	if err != nil {
		return nil, fmt.Errorf("fetching repository names: %w", err)
	}

	// Pre-allocate slice with reasonable capacity (100 repos per page)
	allRepoNames := make([]string, 0, len(pages)*100)

	// Extract repository names
	for _, page := range pages {
		repos := extractRepoNodes(page)
		for _, r := range repos {
			allRepoNames = append(allRepoNames, r["name"].(string))
		}
	}

	return allRepoNames, nil
}

// GetRepoDetails fetches all statistics for a single repository, including basic and detailed stats.
// It uses GraphQL for bulk data fetching and optionally REST for additional details based on fetchConfig.
func GetRepoDetails(ctx context.Context, org, repoName string, packageData map[string]output.PackageCounts, teamAccessMap map[string][]output.RepoTeamAccess, fetchConfig DataFetchConfig) (output.RepoStats, error) {
	// Add per-operation timeout to prevent indefinite hangs on unresponsive API
	// This protects against: network issues, API bugs, extremely slow responses
	opCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// Query for a single repository with all its details
	// This expanded query fetches ~30 additional fields to minimize REST API calls
	query := `
	query($login: String!, $repoName: String!) {
		organization(login: $login) {
			repository(name: $repoName) {
				name
				nameWithOwner
				url
				sshUrl
				homepageUrl
				mirrorUrl
				description
				descriptionHTML
				isFork
				isArchived
				isPrivate
				isTemplate
				isLocked
				isMirror
				isEmpty
				isDisabled
				isSecurityPolicyEnabled
				diskUsage
				createdAt
				updatedAt
				pushedAt
				visibility
				
				# Engagement metrics
				stargazerCount
				forkCount
				watchers(first: 0) {
					totalCount
				}
				
				# Feature flags
				hasWikiEnabled
				hasIssuesEnabled
				hasProjectsEnabled
				hasDiscussionsEnabled
				hasVulnerabilityAlertsEnabled
				hasSponsorshipsEnabled
				
				# Merge settings
				allowUpdateBranch
				deleteBranchOnMerge
				forkingAllowed
				mergeCommitAllowed
				rebaseMergeAllowed
				squashMergeAllowed
				mergeCommitMessage
				mergeCommitTitle
				squashMergeCommitMessage
				squashMergeCommitTitle
				webCommitSignoffRequired
				autoMergeAllowed
				
				# People - Collaborators with permissions (limited to 100, will add pagination if needed)
				collaborators(first: 100) {
					totalCount
					edges {
						permission
						node {
							login
							id
							name
							email
						}
					}
				}
				
				# Branches & refs (count-only, no data needed)
				branches: refs(refPrefix: "refs/heads/", first: 0) {
					totalCount
				}
				tags: refs(refPrefix: "refs/tags/", first: 0) {
					totalCount
				}
				defaultBranchRef {
					name
					target {
						... on Commit {
							oid
							committedDate
						}
					}
				}
				branchProtectionRules(first: 50) {
					totalCount
					nodes {
						id
						pattern
						allowsDeletions
						allowsForcePushes
						blocksCreations
						dismissesStaleReviews
						isAdminEnforced
						lockAllowsFetchAndMerge
						lockBranch
						requireLastPushApproval
						requiredApprovingReviewCount
						requiredDeploymentEnvironments
						requiresApprovingReviews
						requiresCodeOwnerReviews
						requiresCommitSignatures
						requiresConversationResolution
						requiresLinearHistory
						requiresStatusChecks
						requiresStrictStatusChecks
						restrictsPushes
						restrictsReviewDismissals
					}
				}
				
				# Repository rulesets
				rulesets(first: 50) {
					totalCount
					nodes {
						id
						name
						enforcement
						target
						source {
							... on Repository {
								name
							}
						}
					}
				}
				
				# Issues & PRs (count-only, no data needed)
				issues(first: 0) {
					totalCount
				}
				openIssues: issues(states: OPEN, first: 0) {
					totalCount
				}
				closedIssues: issues(states: CLOSED, first: 0) {
					totalCount
				}
				pullRequests(first: 0) {
					totalCount
				}
				openPullRequests: pullRequests(states: OPEN, first: 0) {
					totalCount
				}
				closedPullRequests: pullRequests(states: CLOSED, first: 0) {
					totalCount
				}
				mergedPullRequests: pullRequests(states: MERGED, first: 0) {
					totalCount
				}
				# Labels (count-only, no data needed - totalCount is accurate even with >100)
				labels(first: 0) {
					totalCount
				}
				
				# Milestones with full details
				milestones(first: 50, states: [OPEN, CLOSED]) {
					totalCount
					nodes {
						number
						title
						description
						state
						dueOn
						createdAt
						updatedAt
						closedAt
						openIssueCount: issues(states: OPEN) {
							totalCount
						}
						closedIssueCount: issues(states: CLOSED) {
							totalCount
						}
					}
				}
				
				# Releases with details
				releases(first: 50, orderBy: {field: CREATED_AT, direction: DESC}) {
					totalCount
					nodes {
						id
						name
						tagName
						description
						createdAt
						publishedAt
						isPrerelease
						isDraft
						author {
							login
						}
					}
				}
				latestRelease {
					name
					tagName
					createdAt
					publishedAt
					isPrerelease
					isDraft
				}
				
				# Projects & discussions (count-only, no data needed)
				projectsV2(first: 0) {
					totalCount
				}
				discussions(first: 0) {
					totalCount
				}
				commitComments(first: 0) {
					totalCount
				}
				
				# Languages
				languages(first: 100) {
					totalCount
					totalSize
					edges {
						size
						node {
							name
							color
						}
					}
				}
				primaryLanguage {
					name
					color
				}
				
				# Topics
				repositoryTopics(first: 100) {
					totalCount
					nodes {
						topic {
							name
						}
					}
				}
				
				# License
				licenseInfo {
					name
					key
					spdxId
					url
				}
				
				# Community files
				codeOfConduct {
					name
					key
					url
				}
				contributingGuidelines {
					body
					resourcePath
					url
				}
				codeowners(refName: null) {
					errors {
						kind
						line
						message
						path
					}
				}
				
				# Deploy keys (limited to 100)
				deployKeys(first: 100) {
					totalCount
					nodes {
						id
						key
						title
						readOnly
						createdAt
					}
				}
				
				# Environments (limited to 50)
				environments(first: 50) {
					totalCount
					nodes {
						name
						id
					}
				}
				
				# Deployments with details
				deployments(first: 50, orderBy: {field: CREATED_AT, direction: DESC}) {
					totalCount
					nodes {
						id
						environment
						description
						createdAt
						updatedAt
						state
						latestStatus {
							state
							description
							createdAt
						}
					}
				}
				
				# Vulnerability alerts (count-only, no data needed)
				vulnerabilityAlerts(first: 0) {
					totalCount
				}
				
				# Security policy URL
				securityPolicyUrl
			}
		}
	}`

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

// mergeEnhancedData merges enhanced REST API data with GraphQL-based basic stats
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

// mergeSettings merges REST settings with GraphQL settings
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

// mergeSecurity merges REST security data with GraphQL security data
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

// mergeIssuesData merges REST issues data with GraphQL issues data
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
func buildBasicRepoStats(org string, r map[string]interface{}, packageData map[string]output.PackageCounts, issueEventsCount int) output.RepoStats {
	getString := func(key string) string {
		if val, ok := r[key].(string); ok {
			return val
		}
		return ""
	}
	getBool := func(key string) bool {
		if val, ok := r[key].(bool); ok {
			return val
		}
		return false
	}
	getInt := func(key string) int {
		if val, ok := r[key].(float64); ok {
			return int(val)
		}
		return 0
	}
	getTotalCount := func(key string) int {
		if m, ok := r[key].(map[string]interface{}); ok {
			if val, ok := m["totalCount"].(float64); ok {
				return int(val)
			}
		}
		return 0
	}

	protectedBranches := 0
	if bpr, ok := r["branchProtectionRules"].(map[string]interface{}); ok {
		if val, ok := bpr["totalCount"].(float64); ok {
			protectedBranches = int(val)
		}
	}

	repoName := getString("name")
	repoKey := fmt.Sprintf("%s/%s", org, repoName)
	packageCounts := packageData[repoKey]

	// Extract settings from GraphQL
	settings := extractSettings(r)

	// Extract languages from GraphQL
	languages := extractLanguages(r)

	// Extract topics from GraphQL
	topics := extractTopics(r)

	// Extract license from GraphQL
	license := extractLicense(r)

	// Extract deploy keys from GraphQL
	deployKeys := extractDeployKeys(r)

	// Extract environments from GraphQL
	environments := extractEnvironments(r)

	// Extract labels from GraphQL
	labelsCount := getTotalCount("labels")

	// Extract collaborators from GraphQL
	collaborators := extractCollaborators(r)

	// Extract rulesets from GraphQL
	rulesets := extractRulesets(r)

	// Extract milestones from GraphQL
	milestones := extractMilestones(r)

	// Extract deployments from GraphQL
	deployments := extractDeployments(r)

	// Extract community profile from GraphQL
	communityProfile := extractCommunityProfile(r)

	return output.RepoStats{
		Org:               org,
		Name:              repoName,
		URL:               getString("url"),
		IsFork:            getBool("isFork"),
		IsArchived:        getBool("isArchived"),
		SizeMB:            getInt("diskUsage"),
		HasWiki:           getBool("hasWikiEnabled"),
		CreatedAt:         getString("createdAt"),
		UpdatedAt:         getString("updatedAt"),
		PushedAt:          getString("pushedAt"),
		Stars:             getInt("stargazerCount"),
		Watchers:          getTotalCount("watchers"),
		Forks:             getInt("forkCount"),
		Collaborators:     getTotalCount("collaborators"),
		Contributors:      0, // Will be populated by fetchContributorsCount
		Branches:          getTotalCount("branches"),
		Tags:              getTotalCount("tags"),
		ProtectedBranches: protectedBranches,
		Issues:            getTotalCount("issues"),
		OpenIssues:        getTotalCount("openIssues"),
		ClosedIssues:      getTotalCount("closedIssues"),
		PRs:               getTotalCount("pullRequests"),
		OpenPRs:           getTotalCount("openPullRequests"),
		ClosedPRs:         getTotalCount("closedPullRequests"),
		MergedPRs:         getTotalCount("mergedPullRequests"),
		Commits:           0, // Will be populated by fetchCommitCount
		Milestones:        getTotalCount("milestones"),
		Releases:          getTotalCount("releases"),
		Projects:          getTotalCount("projectsV2"),
		Discussions:       getTotalCount("discussions"),
		CommitComments:    getTotalCount("commitComments"),
		IssueEvents:       issueEventsCount,
		Packages:          packageCounts.PackageCount,
		PackageVersions:   packageCounts.PackageVersions,

		// Data from GraphQL (eliminates REST calls)
		Settings:              settings,
		Languages:             languages,
		Topics:                topics,
		License:               license,
		DeployKeys:            deployKeys,
		Environments:          environments,
		CollaboratorsDetailed: collaborators,
		Rulesets:              rulesets,
		Deployments:           deployments,
		CommunityProfile:      communityProfile,
		IssuesData: &output.IssuesData{
			LabelsCount:     labelsCount,
			MilestonesCount: len(milestones),
			Milestones:      milestones,
		},
		Security: &output.RepoSecurity{
			// Will be merged from REST API data for Dependabot, CodeScanning, SecretScanning details
		},
	}
}

// extractRepoPagination extracts the endCursor and hasNextPage from the GraphQL response.
func extractRepoPagination(data map[string]interface{}) (string, bool) {
	// GraphQL responses are wrapped in a "data" key
	dataObj, ok := data["data"].(map[string]interface{})
	if !ok {
		return "", false
	}

	org, ok := dataObj["organization"].(map[string]interface{})
	if !ok {
		return "", false
	}
	repos, ok := org["repositories"].(map[string]interface{})
	if !ok {
		return "", false
	}
	pageInfo, ok := repos["pageInfo"].(map[string]interface{})
	if !ok {
		return "", false
	}
	endCursor, _ := pageInfo["endCursor"].(string)
	hasNextPage, _ := pageInfo["hasNextPage"].(bool)
	return endCursor, hasNextPage
}

// extractRepoNodes extracts the repository nodes from the GraphQL response.
func extractRepoNodes(data map[string]interface{}) []map[string]interface{} {
	// Try with "data" wrapper first (standard GraphQL response)
	if dataObj, ok := data["data"].(map[string]interface{}); ok {
		data = dataObj
	}

	org, ok := data["organization"].(map[string]interface{})
	if !ok {
		fmt.Printf("DEBUG: 'organization' key not found. Keys in response: %v\n", getKeys(data))
		return nil
	}
	repos, ok := org["repositories"].(map[string]interface{})
	if !ok {
		fmt.Printf("DEBUG: 'repositories' key not found. Keys in org: %v\n", getKeys(org))
		return nil
	}
	nodes, ok := repos["nodes"].([]interface{})
	if !ok {
		fmt.Printf("DEBUG: 'nodes' key not found. Keys in repos: %v\n", getKeys(repos))
		return nil
	}
	result := make([]map[string]interface{}, 0, len(nodes))
	for _, n := range nodes {
		if m, ok := n.(map[string]interface{}); ok {
			result = append(result, m)
		}
	}
	return result
}

// Helper function to get keys from a map for debugging
func getKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// extractSettings extracts repository settings from GraphQL response
func extractSettings(r map[string]interface{}) *output.RepoSettings {
	getBool := func(key string) bool {
		if val, ok := r[key].(bool); ok {
			return val
		}
		return false
	}
	getString := func(key string) string {
		if val, ok := r[key].(string); ok {
			return val
		}
		return ""
	}

	return &output.RepoSettings{
		Visibility:               getString("visibility"),
		HasIssues:                getBool("hasIssuesEnabled"),
		HasProjects:              getBool("hasProjectsEnabled"),
		HasWiki:                  getBool("hasWikiEnabled"),
		HasDiscussions:           getBool("hasDiscussionsEnabled"),
		AllowSquashMerge:         getBool("squashMergeAllowed"),
		AllowMergeCommit:         getBool("mergeCommitAllowed"),
		AllowRebaseMerge:         getBool("rebaseMergeAllowed"),
		AllowAutoMerge:           getBool("autoMergeAllowed"),
		DeleteBranchOnMerge:      getBool("deleteBranchOnMerge"),
		AllowForking:             getBool("forkingAllowed"),
		WebCommitSignoffRequired: getBool("webCommitSignoffRequired"),
		// DefaultBranch will be populated from REST API
	}
}

// extractLanguages extracts language data from GraphQL response
func extractLanguages(r map[string]interface{}) map[string]int {
	langs := make(map[string]int)

	if langsData, ok := r["languages"].(map[string]interface{}); ok {
		if edges, ok := langsData["edges"].([]interface{}); ok {
			for _, edge := range edges {
				if e, ok := edge.(map[string]interface{}); ok {
					size := 0
					if s, ok := e["size"].(float64); ok {
						size = int(s)
					}
					if node, ok := e["node"].(map[string]interface{}); ok {
						if name, ok := node["name"].(string); ok {
							langs[name] = size
						}
					}
				}
			}
		}
	}

	return langs
}

// extractTopics extracts repository topics from GraphQL response
func extractTopics(r map[string]interface{}) []string {
	topics := []string{}

	if topicsData, ok := r["repositoryTopics"].(map[string]interface{}); ok {
		if nodes, ok := topicsData["nodes"].([]interface{}); ok {
			for _, node := range nodes {
				if n, ok := node.(map[string]interface{}); ok {
					if topic, ok := n["topic"].(map[string]interface{}); ok {
						if name, ok := topic["name"].(string); ok {
							topics = append(topics, name)
						}
					}
				}
			}
		}
	}

	return topics
}

// extractLicense extracts license information from GraphQL response
func extractLicense(r map[string]interface{}) *output.License {
	if licenseInfo, ok := r["licenseInfo"].(map[string]interface{}); ok {
		getString := func(key string) string {
			if val, ok := licenseInfo[key].(string); ok {
				return val
			}
			return ""
		}

		return &output.License{
			Key:    getString("key"),
			Name:   getString("name"),
			SPDXID: getString("spdxId"),
			URL:    getString("url"),
		}
	}

	return nil
}

// extractDeployKeys extracts deploy keys from GraphQL response
func extractDeployKeys(r map[string]interface{}) []output.DeployKey {
	keys := []output.DeployKey{}

	if deployKeysData, ok := r["deployKeys"].(map[string]interface{}); ok {
		if nodes, ok := deployKeysData["nodes"].([]interface{}); ok {
			for _, node := range nodes {
				if n, ok := node.(map[string]interface{}); ok {
					getString := func(key string) string {
						if val, ok := n[key].(string); ok {
							return val
						}
						return ""
					}
					getBool := func(key string) bool {
						if val, ok := n[key].(bool); ok {
							return val
						}
						return false
					}
					// GraphQL returns id as string, but we need int64
					// For now, just use the key as ID
					id := int64(0)

					keys = append(keys, output.DeployKey{
						ID:        id,
						Key:       getString("key"),
						Title:     getString("title"),
						ReadOnly:  getBool("readOnly"),
						CreatedAt: getString("createdAt"),
					})
				}
			}
		}
	}

	return keys
}

// extractEnvironments extracts environment data from GraphQL response
func extractEnvironments(r map[string]interface{}) []output.Environment {
	envs := []output.Environment{}

	if envsData, ok := r["environments"].(map[string]interface{}); ok {
		if nodes, ok := envsData["nodes"].([]interface{}); ok {
			for _, node := range nodes {
				if n, ok := node.(map[string]interface{}); ok {
					getString := func(key string) string {
						if val, ok := n[key].(string); ok {
							return val
						}
						return ""
					}
					// GraphQL returns id as string, but we need int64
					id := int64(0)

					envs = append(envs, output.Environment{
						Name: getString("name"),
						ID:   id,
					})
				}
			}
		}
	}

	return envs
}

// extractCollaborators extracts collaborator data with permissions from GraphQL response
func extractCollaborators(r map[string]interface{}) []output.RepoCollaborator {
	collabs := []output.RepoCollaborator{}

	if collabsData, ok := r["collaborators"].(map[string]interface{}); ok {
		if edges, ok := collabsData["edges"].([]interface{}); ok {
			for _, edge := range edges {
				if e, ok := edge.(map[string]interface{}); ok {
					permission := ""
					if p, ok := e["permission"].(string); ok {
						permission = p
					}

					if node, ok := e["node"].(map[string]interface{}); ok {
						getString := func(key string) string {
							if val, ok := node[key].(string); ok {
								return val
							}
							return ""
						}

						collab := output.RepoCollaborator{
							Login: getString("login"),
							ID:    0, // GraphQL returns string ID
						}

						// Map GraphQL permission to struct
						switch permission {
						case "ADMIN":
							collab.Permissions.Admin = true
						case "MAINTAIN":
							collab.Permissions.Maintain = true
						case "WRITE":
							collab.Permissions.Push = true
						case "TRIAGE":
							collab.Permissions.Triage = true
						case "READ":
							collab.Permissions.Pull = true
						}

						collabs = append(collabs, collab)
					}
				}
			}
		}
	}

	return collabs
}

// extractRulesets extracts repository rulesets from GraphQL response
func extractRulesets(r map[string]interface{}) []output.Ruleset {
	rulesets := []output.Ruleset{}

	if rulesetsData, ok := r["rulesets"].(map[string]interface{}); ok {
		if nodes, ok := rulesetsData["nodes"].([]interface{}); ok {
			for _, node := range nodes {
				if n, ok := node.(map[string]interface{}); ok {
					getString := func(key string) string {
						if val, ok := n[key].(string); ok {
							return val
						}
						return ""
					}

					rulesets = append(rulesets, output.Ruleset{
						ID:          0, // GraphQL returns string ID
						Name:        getString("name"),
						Enforcement: getString("enforcement"),
						Target:      getString("target"),
					})
				}
			}
		}
	}

	return rulesets
}

// extractMilestones extracts milestone data with issue counts from GraphQL response
func extractMilestones(r map[string]interface{}) []output.Milestone {
	milestones := []output.Milestone{}

	if milestonesData, ok := r["milestones"].(map[string]interface{}); ok {
		if nodes, ok := milestonesData["nodes"].([]interface{}); ok {
			for _, node := range nodes {
				if n, ok := node.(map[string]interface{}); ok {
					getString := func(key string) string {
						if val, ok := n[key].(string); ok {
							return val
						}
						return ""
					}
					getInt := func(key string) int {
						if val, ok := n[key].(float64); ok {
							return int(val)
						}
						return 0
					}
					getTotalCountFromObj := func(key string) int {
						if m, ok := n[key].(map[string]interface{}); ok {
							if val, ok := m["totalCount"].(float64); ok {
								return int(val)
							}
						}
						return 0
					}

					milestones = append(milestones, output.Milestone{
						Number:       getInt("number"),
						Title:        getString("title"),
						Description:  getString("description"),
						State:        getString("state"),
						OpenIssues:   getTotalCountFromObj("openIssueCount"),
						ClosedIssues: getTotalCountFromObj("closedIssueCount"),
						DueOn:        getString("dueOn"),
						CreatedAt:    getString("createdAt"),
						UpdatedAt:    getString("updatedAt"),
						ClosedAt:     getString("closedAt"),
					})
				}
			}
		}
	}

	return milestones
}

// extractDeployments extracts deployment data from GraphQL response
func extractDeployments(r map[string]interface{}) []output.Deployment {
	deployments := []output.Deployment{}

	if deploymentsData, ok := r["deployments"].(map[string]interface{}); ok {
		if nodes, ok := deploymentsData["nodes"].([]interface{}); ok {
			for _, node := range nodes {
				if n, ok := node.(map[string]interface{}); ok {
					getString := func(key string) string {
						if val, ok := n[key].(string); ok {
							return val
						}
						return ""
					}

					var latestStatus *output.DeploymentStatus
					if ls, ok := n["latestStatus"].(map[string]interface{}); ok {
						state := ""
						desc := ""
						if s, ok := ls["state"].(string); ok {
							state = s
						}
						if d, ok := ls["description"].(string); ok {
							desc = d
						}
						latestStatus = &output.DeploymentStatus{
							State:       state,
							Description: desc,
						}
					}

					deployments = append(deployments, output.Deployment{
						ID:           0, // GraphQL returns string ID
						Environment:  getString("environment"),
						Description:  getString("description"),
						CreatedAt:    getString("createdAt"),
						UpdatedAt:    getString("updatedAt"),
						LatestStatus: latestStatus,
					})
				}
			}
		}
	}

	return deployments
}

// extractCommunityProfile extracts community profile data from GraphQL response
func extractCommunityProfile(r map[string]interface{}) *output.CommunityProfile {
	profile := &output.CommunityProfile{
		Files: &output.CommunityProfileFiles{},
	}

	// Code of Conduct
	if coc, ok := r["codeOfConduct"].(map[string]interface{}); ok {
		if url, ok := coc["url"].(string); ok {
			profile.Files.CodeOfConduct = &output.CommunityFile{
				URL: url,
			}
		}
	}

	// Contributing Guidelines
	if contrib, ok := r["contributingGuidelines"].(map[string]interface{}); ok {
		if url, ok := contrib["url"].(string); ok {
			profile.Files.Contributing = &output.CommunityFile{
				URL: url,
			}
		}
	}

	return profile
}
