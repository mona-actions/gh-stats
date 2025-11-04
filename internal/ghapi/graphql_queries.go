// Package ghapi provides GitHub API client functionality.
//
// This file (graphql_queries.go) contains GraphQL query building functions.
// It provides functions that dynamically construct GraphQL queries based on DataFetchConfig,
// allowing selective fetching of repository data to minimize API calls.
package ghapi

import (
	"context"
	"fmt"
	"strings"

	"github.com/pterm/pterm"
)

func getBaseOnlyConfig(original DataFetchConfig) DataFetchConfig {
	return DataFetchConfig{
		// Keep these cheap fields that work well in batches
		FetchSettings:    original.FetchSettings,
		FetchCustomProps: original.FetchCustomProps,

		// Exclude expensive GraphQL fields from batching
		// These will be fetched per-repo separately if enabled
		FetchCollaborators:  false,
		FetchRulesets:       false,
		FetchMilestones:     false,
		FetchLanguages:      false,
		FetchTopics:         false,
		FetchLicense:        false,
		FetchDeployKeys:     false,
		FetchEnvironments:   false,
		FetchDeployments:    false,
		FetchCommunityFiles: false,

		// REST API fields - keep as-is, handled separately anyway
		FetchBranches:         original.FetchBranches,
		FetchWebhooks:         original.FetchWebhooks,
		FetchAutolinks:        original.FetchAutolinks,
		FetchActions:          original.FetchActions,
		FetchSecurity:         original.FetchSecurity,
		FetchPages:            original.FetchPages,
		FetchIssuesData:       original.FetchIssuesData,
		FetchPRsData:          original.FetchPRsData,
		FetchTraffic:          original.FetchTraffic,
		FetchTags:             original.FetchTags,
		FetchGitRefs:          original.FetchGitRefs,
		FetchLFS:              original.FetchLFS,
		FetchFiles:            original.FetchFiles,
		FetchContributors:     original.FetchContributors,
		FetchCommits:          original.FetchCommits,
		FetchIssueEvents:      original.FetchIssueEvents,
		FetchBranchProtection: original.FetchBranchProtection,
		FetchReleases:         original.FetchReleases,
	}
}

// getExpensiveOnlyConfig returns a config with only expensive GraphQL fields enabled.
// This is used to fetch expensive fields separately per-repo after batching.
func getExpensiveOnlyConfig(original DataFetchConfig) DataFetchConfig {
	return DataFetchConfig{
		// Only expensive GraphQL fields that benefit from separate fetching
		FetchCollaborators:  original.FetchCollaborators,
		FetchRulesets:       original.FetchRulesets,
		FetchMilestones:     original.FetchMilestones,
		FetchLanguages:      original.FetchLanguages,
		FetchTopics:         original.FetchTopics,
		FetchLicense:        original.FetchLicense,
		FetchDeployKeys:     original.FetchDeployKeys,
		FetchEnvironments:   original.FetchEnvironments,
		FetchDeployments:    original.FetchDeployments,
		FetchCommunityFiles: original.FetchCommunityFiles,

		// Note: Branch protection and releases are not included here as they're
		// either handled via REST or are just counts, not detailed data

		// Disable everything else
		FetchSettings:         false,
		FetchCustomProps:      false,
		FetchBranches:         false,
		FetchWebhooks:         false,
		FetchAutolinks:        false,
		FetchActions:          false,
		FetchSecurity:         false,
		FetchPages:            false,
		FetchIssuesData:       false,
		FetchPRsData:          false,
		FetchTraffic:          false,
		FetchTags:             false,
		FetchGitRefs:          false,
		FetchLFS:              false,
		FetchFiles:            false,
		FetchContributors:     false,
		FetchCommits:          false,
		FetchIssueEvents:      false,
		FetchBranchProtection: false,
		FetchReleases:         false,
	}
}

// hasExpensiveFields checks if any expensive GraphQL fields are enabled.
func hasExpensiveFields(config DataFetchConfig) bool {
	return config.FetchCollaborators ||
		config.FetchRulesets ||
		config.FetchMilestones ||
		config.FetchLanguages ||
		config.FetchTopics ||
		config.FetchLicense ||
		config.FetchDeployKeys ||
		config.FetchEnvironments ||
		config.FetchDeployments ||
		config.FetchCommunityFiles
}

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
		// Note: API call counter incremented by executeGraphQLPage, not here
		return extractRepoPagination(data)
	})

	// If we got partial results (some pages succeeded before error), process them
	if err != nil && len(pages) == 0 {
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

	// If we got partial results, return them with a warning
	if err != nil {
		pterm.Warning.Printf("⚠ Incomplete repository list: fetched %d repos but pagination failed: %v\n", len(allRepoNames), err)
		return allRepoNames, nil // Return partial results without error
	}

	return allRepoNames, nil
}

// buildRepositoryQuery builds a GraphQL query for repository details based on fetchConfig.
// This allows controlling query complexity by conditionally including expensive fields.
func buildRepositoryQuery(config DataFetchConfig) string {
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
				
				# Engagement metrics (always fetched - cheap count fields)
				stargazerCount
				forkCount
				watchers(first: 0) {
					totalCount
				}
				
				# Feature flags (always fetched - simple boolean fields)
				hasWikiEnabled
				hasIssuesEnabled
				hasProjectsEnabled
				hasDiscussionsEnabled
				hasVulnerabilityAlertsEnabled
				hasSponsorshipsEnabled
				
				# Merge settings (always fetched - simple fields)
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
				
				# Branches & refs (always fetched - cheap count fields)
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
				
				# Issues & PRs counts (always fetched - cheap count fields)
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
				labels(first: 0) {
					totalCount
				}
				
				# Projects & discussions counts (always fetched - cheap count fields)
				projectsV2(first: 0) {
					totalCount
				}
				discussions(first: 0) {
					totalCount
				}
				commitComments(first: 0) {
					totalCount
				}
				
				# Vulnerability alerts count (always fetched - cheap count field)
				vulnerabilityAlerts(first: 0) {
					totalCount
				}
				
				# Security policy URL (always fetched - simple field)
				securityPolicyUrl
`

	// Conditionally add expensive fields based on fetchConfig

	if config.FetchCollaborators {
		query += `
				# Collaborators with permissions (expensive - up to 100 users with nested data)
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
`
	}

	if config.FetchBranchProtection {
		query += `
				# Branch protection rules (expensive - up to 50 rules with many fields)
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
`
	}

	if config.FetchRulesets {
		query += `
				# Repository rulesets (expensive - up to 50 rulesets with nested data)
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
`
	}

	if config.FetchMilestones {
		query += `
				# Milestones with issue counts (expensive - up to 50 milestones with nested counts)
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
`
	}

	if config.FetchReleases {
		query += `
				# Releases with details (expensive - up to 50 releases with author data)
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
`
	}

	if config.FetchLanguages {
		query += `
				# Languages (moderate - up to 100 languages with sizes)
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
`
	}

	if config.FetchTopics {
		query += `
				# Topics (moderate - up to 100 topics)
				repositoryTopics(first: 100) {
					totalCount
					nodes {
						topic {
							name
						}
					}
				}
`
	}

	if config.FetchLicense {
		query += `
				# License (cheap - single object)
				licenseInfo {
					name
					key
					spdxId
					url
				}
`
	}

	if config.FetchCommunityFiles {
		query += `
				# Community files (moderate - multiple nested objects)
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
`
	}

	if config.FetchDeployKeys {
		query += `
				# Deploy keys (expensive - up to 100 keys with details)
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
`
	}

	if config.FetchEnvironments {
		query += `
				# Environments (moderate - up to 50 environments)
				environments(first: 50) {
					totalCount
					nodes {
						name
						id
					}
				}
`
	}

	if config.FetchDeployments {
		query += `
				# Deployments (expensive - up to 50 deployments with nested status)
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
`
	}

	// Close the query
	query += `
			}
		}
	}`

	return query
}

// buildBatchedRepositoryQuery builds a GraphQL query to fetch multiple repositories in a single request.
// Uses aliases to fetch multiple repos: repo1: repository(name: "repo1") { ... }.
func buildBatchedRepositoryQuery(repoNames []string, config DataFetchConfig) string {
	// Start building the batched query
	var builder strings.Builder
	builder.WriteString(`query($login: String!) {
		organization(login: $login) {
`)

	// Add each repository with a unique alias
	fields := getRepositoryFields(config)
	for i, repoName := range repoNames {
		// Create a safe alias (GraphQL aliases can't start with numbers or have hyphens)
		alias := fmt.Sprintf("repo_%d", i)

		// Add this repository with an alias
		fmt.Fprintf(&builder, "			%s: repository(name: %q) {\n", alias, repoName)
		builder.WriteString(fields)
		builder.WriteString("			}\n")
	}

	// Close the organization and query
	builder.WriteString(`		}
	}`)

	return builder.String()
}

// getRepositoryFields returns just the repository field definitions for use in batched queries.
// This extracts the core fields that buildRepositoryQuery uses, without the query wrapper.
func getRepositoryFields(config DataFetchConfig) string {
	fields := `				name
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
				mergeCommitAllowed
				squashMergeAllowed
				rebaseMergeAllowed
				autoMergeAllowed
				deleteBranchOnMerge
				allowUpdateBranch
				webCommitSignoffRequired
				mergeCommitTitle
				mergeCommitMessage
				squashMergeCommitTitle
				squashMergeCommitMessage
				
				# Counts (cheap - no data transfer, just counts)
				refs(refPrefix: "refs/heads/", first: 0) {
					totalCount
				}
				tags: refs(refPrefix: "refs/tags/", first: 0) {
					totalCount
				}
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
				labels(first: 0) {
					totalCount
				}
				projectsV2(first: 0) {
					totalCount
				}
				discussions(first: 0) {
					totalCount
				}
				commitComments(first: 0) {
					totalCount
				}
				vulnerabilityAlerts(first: 0) {
					totalCount
				}
				securityPolicyUrl
				
				# Default branch info
				defaultBranchRef {
					name
					target {
						... on Commit {
							oid
							committedDate
						}
					}
				}
`

	// Add optional expensive fields based on config
	if config.FetchCollaborators {
		fields += `
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
`
	}

	if config.FetchBranchProtection {
		fields += `
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
`
	}

	if config.FetchRulesets {
		fields += `
				rulesets(first: 50) {
					totalCount
					nodes {
						id
						name
						enforcement
						target
						sourceType
						createdAt
						updatedAt
					}
				}
`
	}

	if config.FetchMilestones {
		fields += `
				milestones(first: 50, states: [OPEN, CLOSED]) {
					totalCount
					nodes {
						title
						state
						number
						description
						dueOn
						createdAt
						updatedAt
						closedAt
						issues(first: 0) {
							totalCount
						}
						openIssues: issues(states: OPEN, first: 0) {
							totalCount
						}
						closedIssues: issues(states: CLOSED, first: 0) {
							totalCount
						}
					}
				}
`
	}

	if config.FetchReleases {
		fields += `
				releases(first: 50, orderBy: {field: CREATED_AT, direction: DESC}) {
					totalCount
					nodes {
						name
						tagName
						isPrerelease
						isDraft
						createdAt
						publishedAt
						author {
							login
						}
					}
				}
`
	}

	if config.FetchLanguages {
		fields += `
				languages(first: 100) {
					totalSize
					edges {
						size
						node {
							name
							color
						}
					}
				}
`
	}

	if config.FetchTopics {
		fields += `
				repositoryTopics(first: 100) {
					totalCount
					nodes {
						topic {
							name
						}
					}
				}
`
	}

	if config.FetchLicense {
		fields += `
				licenseInfo {
					name
					key
					spdxId
					url
				}
`
	}

	if config.FetchCommunityFiles {
		fields += `
				codeOfConduct {
					name
					key
					url
				}
				contributing: object(expression: "HEAD:CONTRIBUTING.md") {
					... on Blob {
						byteSize
					}
				}
				codeowners: object(expression: "HEAD:.github/CODEOWNERS") {
					... on Blob {
						byteSize
					}
				}
`
	}

	if config.FetchDeployKeys {
		fields += `
				deployKeys(first: 100) {
					totalCount
					nodes {
						id
						key
						title
						readOnly
						verified
						createdAt
					}
				}
`
	}

	if config.FetchEnvironments {
		fields += `
				environments(first: 50) {
					totalCount
					nodes {
						name
						id
					}
				}
`
	}

	if config.FetchDeployments {
		fields += `
				deployments(first: 50, orderBy: {field: CREATED_AT, direction: DESC}) {
					totalCount
					nodes {
						environment
						createdAt
						updatedAt
						ref {
							name
						}
						creator {
							login
						}
						state
						latestStatus {
							state
							description
							createdAt
						}
					}
				}
`
	}

	return fields
}

// GetRepoDetails fetches all statistics for a single repository, including basic and detailed stats.
// It uses GraphQL for bulk data fetching and optionally REST for additional details based on fetchConfig.
