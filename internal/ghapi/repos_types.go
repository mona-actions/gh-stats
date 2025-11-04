// Package ghapi provides GitHub API client functionality.
//
// This file (repos_types.go) contains repository-level type definitions and configurations.
// It defines DataFetchConfig which controls which data categories to fetch from the GitHub API.
//
// Key features:
//   - Feature flag-based data fetching control
//   - Fine-grained API usage optimization
//   - Support for both REST and GraphQL API features
//   - Comprehensive configuration for all data categories
package ghapi

// DataFetchConfig controls which data to fetch to reduce API calls.
//
// Each boolean field enables or disables fetching a specific category of data.
// This allows fine-grained control over API usage and execution time.
//
// Zero value behavior:
//   - All boolean fields default to false (disabled)
//   - Use DefaultDataFetchConfig() to get all features enabled.
//
// Thread-safety: This type is safe for concurrent reads but should not be.
// modified after being passed to data fetching functions.
//
// Example:
//
//	config := DataFetchConfig{
//	    FetchActions: true,   // Enable Actions data.
//	    FetchSecurity: true,  // Enable security scanning.
//	    // All other fields default to false (disabled)
//	}
type DataFetchConfig struct {
	FetchSettings     bool
	FetchCustomProps  bool
	FetchBranches     bool
	FetchWebhooks     bool
	FetchAutolinks    bool
	FetchActions      bool
	FetchSecurity     bool
	FetchPages        bool
	FetchIssuesData   bool
	FetchPRsData      bool
	FetchTraffic      bool
	FetchTags         bool
	FetchGitRefs      bool
	FetchLFS          bool
	FetchFiles        bool
	FetchContributors bool
	FetchCommits      bool
	FetchIssueEvents  bool

	// GraphQL-specific feature flags (control query complexity)
	FetchCollaborators    bool
	FetchLanguages        bool
	FetchTopics           bool
	FetchLicense          bool
	FetchDeployKeys       bool
	FetchEnvironments     bool
	FetchDeployments      bool
	FetchMilestones       bool
	FetchReleases         bool
	FetchCommunityFiles   bool
	FetchRulesets         bool
	FetchBranchProtection bool
}

// DefaultDataFetchConfig returns a config with all features enabled.
func DefaultDataFetchConfig() DataFetchConfig {
	return DataFetchConfig{
		FetchSettings:     true,
		FetchCustomProps:  true,
		FetchBranches:     true,
		FetchWebhooks:     true,
		FetchAutolinks:    true,
		FetchActions:      true,
		FetchSecurity:     true,
		FetchPages:        true,
		FetchIssuesData:   true,
		FetchPRsData:      true,
		FetchTraffic:      true,
		FetchTags:         true,
		FetchGitRefs:      true,
		FetchLFS:          true,
		FetchFiles:        true,
		FetchContributors: true,
		FetchCommits:      true,
		FetchIssueEvents:  true,

		// GraphQL-specific features
		FetchCollaborators:    true,
		FetchLanguages:        true,
		FetchTopics:           true,
		FetchLicense:          true,
		FetchDeployKeys:       true,
		FetchEnvironments:     true,
		FetchDeployments:      true,
		FetchMilestones:       true,
		FetchReleases:         true,
		FetchCommunityFiles:   true,
		FetchRulesets:         true,
		FetchBranchProtection: true,
	}
}
