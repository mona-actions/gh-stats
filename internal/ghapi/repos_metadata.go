// Package ghapi provides GitHub API client functionality.
//
// This file (repos_metadata.go) contains REST API functions for fetching repository data.
// It provides comprehensive functions to fetch repository metadata, settings,
// automation data, security data, issues, PRs, and other repository features.
package ghapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mona-actions/gh-stats/internal/output"
	"github.com/mona-actions/gh-stats/internal/state"
	"github.com/pterm/pterm"
)

func GetIssueEventsCount(ctx context.Context, org, repo string) (int, error) {
	endpoint := fmt.Sprintf("/repos/%s/%s/issues/events", org, repo)
	count, err := getCountFromLinkHeader(ctx, endpoint)
	if err != nil {
		// Non-critical metric - return 0 instead of propagating error
		return 0, nil
	}
	return count, nil
}

// ============================================================================
// Phase 1: Repository Settings & Access Control Data Collection
// ============================================================================

// GetEnhancedRepoData fetches comprehensive repository-level data for migration.
//
// This function orchestrates the collection of repository data across multiple GitHub API
// endpoints. It uses both REST and GraphQL APIs to gather settings, access controls,
// branches, webhooks, security configurations, and more.
//
// Parameters:
//   - ctx: Context for cancellation (respects parent timeout/cancellation)
//   - org: Organization name (owner of the repository)
//   - repo: Repository name.
//   - teamAccessMap: Optional pre-fetched team access data (if nil, will fetch separately)
//   - fetchConfig: Configuration controlling which data to fetch (feature flags)
//
// Returns:
//   - *output.RepoStats: Comprehensive repository statistics and metadata.
//   - error: Only on critical failures that prevent basic repo information retrieval.
//
// Error Handling Strategy:
//   - Most API errors are logged as warnings but don't fail the operation.
//   - Only critical failures (unable to get core repo data) return errors.
//   - Non-critical failures (webhooks, actions, etc.) are logged and processing continues.
//   - This resilient approach ensures maximum data collection even with partial API failures.
//
// Possible Errors:
//   - context.Canceled if operation is cancelled by user.
//   - context.DeadlineExceeded if operation times out.
//   - API authentication errors (401/403) if token lacks required permissions.
//   - API rate limit errors (though should be checked before calling this function)
//   - Network errors for core repository data fetch.
//
// Side Effects:
//   - Makes multiple GitHub API calls (count varies based on fetchConfig)
//   - Updates global API call counter.
//   - Prints warning messages to stdout for non-critical failures.
//   - May trigger rate limit consumption tracking.
//
// fetchRepoSettingsAndAccess fetches repository settings and access control data.
func fetchRepoSettingsAndAccess(ctx context.Context, org, repo string, teamAccessMap map[string][]output.RepoTeamAccess, fetchConfig DataFetchConfig, stats *output.RepoStats) {
	if fetchConfig.FetchSettings {
		if err := fetchRepoSettings(ctx, org, repo, stats); err != nil {
			pterm.Warning.Printf("Failed to fetch additional repo settings for %s/%s: %v\n", org, repo, err)
		}
	}

	// Use pre-fetched team access data if available, otherwise fetch it
	if teamAccessMap != nil {
		if teamAccess, ok := teamAccessMap[repo]; ok {
			stats.TeamAccess = teamAccess
		}
	} else if err := fetchTeamAccess(ctx, org, repo, stats); err != nil {
		pterm.Warning.Printf("Failed to fetch team access for %s/%s: %v\n", org, repo, err)
	}

	if fetchConfig.FetchCustomProps {
		if err := fetchCustomPropertiesForRepo(ctx, org, repo, stats); err != nil {
			pterm.Warning.Printf("Failed to fetch custom properties for %s/%s: %v\n", org, repo, err)
		}
	}

	if fetchConfig.FetchBranches {
		if err := fetchBranches(ctx, org, repo, stats); err != nil {
			pterm.Warning.Printf("Failed to fetch branches for %s/%s: %v\n", org, repo, err)
		}
	}
}

// ============================================================================
// Phase 2: Automation & CI/CD Data Collection
// ============================================================================

// fetchRepoAutomationData fetches webhooks, autolinks, and Actions data.
func fetchRepoAutomationData(ctx context.Context, org, repo string, fetchConfig DataFetchConfig, stats *output.RepoStats) {
	if fetchConfig.FetchWebhooks {
		if err := fetchRepoWebhooks(ctx, org, repo, stats); err != nil {
			pterm.Warning.Printf("Failed to fetch webhooks for %s/%s: %v\n", org, repo, err)
		}
	}

	if fetchConfig.FetchAutolinks {
		if err := fetchAutolinks(ctx, org, repo, stats); err != nil {
			pterm.Warning.Printf("Failed to fetch autolinks for %s/%s: %v\n", org, repo, err)
		}
	}

	if fetchConfig.FetchActions {
		if err := fetchActionsData(ctx, org, repo, stats); err != nil {
			pterm.Warning.Printf("Failed to fetch Actions data for %s/%s: %v\n", org, repo, err)
		}
	}
}

// fetchRepoIssuesAndPRs fetches issues and pull request data.
func fetchRepoIssuesAndPRs(ctx context.Context, org, repo string, fetchConfig DataFetchConfig, stats *output.RepoStats) {
	if fetchConfig.FetchSecurity {
		if err := fetchSecurityData(ctx, org, repo, stats); err != nil {
			pterm.Warning.Printf("Failed to fetch security data for %s/%s: %v\n", org, repo, err)
		}
	}

	if fetchConfig.FetchPages {
		_ = fetchPages(ctx, org, repo, stats) // Many repos don't have pages - don't warn
	}

	if fetchConfig.FetchIssuesData {
		if err := fetchIssuesData(ctx, org, repo, stats); err != nil {
			pterm.Warning.Printf("Failed to fetch issues data for %s/%s: %v\n", org, repo, err)
		}
	}

	if fetchConfig.FetchPRsData {
		if err := fetchPullRequestsData(ctx, org, repo, stats); err != nil {
			pterm.Warning.Printf("Failed to fetch pull requests data for %s/%s: %v\n", org, repo, err)
		}
	}

	if fetchConfig.FetchTraffic {
		_ = fetchTrafficData(ctx, org, repo, stats) // Traffic data often unavailable - don't warn
	}
}

// fetchRepoGitMetadata fetches tags, refs, LFS, and file data.
func fetchRepoGitMetadata(ctx context.Context, org, repo string, fetchConfig DataFetchConfig, stats *output.RepoStats) {
	if fetchConfig.FetchTags {
		_ = fetchTagsDetailed(ctx, org, repo, stats) // Tags might not be available - don't warn
	}

	if fetchConfig.FetchGitRefs {
		_ = fetchGitReferences(ctx, org, repo, stats) // Git refs might not be available - don't warn
	}

	if fetchConfig.FetchLFS {
		_ = fetchGitLFSStatus(ctx, org, repo, stats) // LFS status might not be available - don't warn
	}

	if fetchConfig.FetchFiles {
		_ = fetchRepoFiles(ctx, org, repo, stats) // Repo files might not be available - don't warn
	}
}

// fetchRepoEngagementMetrics fetches contributor and commit data.
func fetchRepoEngagementMetrics(ctx context.Context, org, repo string, fetchConfig DataFetchConfig, stats *output.RepoStats) {
	if fetchConfig.FetchContributors {
		_ = fetchContributorsCount(ctx, org, repo, stats) // Contributors might not be available - don't warn
	}

	if fetchConfig.FetchCommits {
		_ = fetchCommitCount(ctx, org, repo, stats) // Commit count might not be available - don't warn
	}
}

// GetEnhancedRepoData fetches comprehensive repository data using both GraphQL and REST APIs.
// This is the main coordinator function that delegates to specific fetch functions based on feature flags.
func GetEnhancedRepoData(ctx context.Context, org, repo string, teamAccessMap map[string][]output.RepoTeamAccess, fetchConfig DataFetchConfig) (*output.RepoStats, error) {
	stats := &output.RepoStats{
		Org:  org,
		Name: repo,
	}

	// Fetch different categories of data
	fetchRepoSettingsAndAccess(ctx, org, repo, teamAccessMap, fetchConfig, stats)
	fetchRepoAutomationData(ctx, org, repo, fetchConfig, stats)
	fetchRepoIssuesAndPRs(ctx, org, repo, fetchConfig, stats)
	fetchRepoGitMetadata(ctx, org, repo, fetchConfig, stats)
	fetchRepoEngagementMetrics(ctx, org, repo, fetchConfig, stats)

	return stats, nil
}

// fetchRepoSettings fetches repository settings and configuration.
func fetchRepoSettings(ctx context.Context, org, repo string, stats *output.RepoStats) error {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	endpoint := fmt.Sprintf("/repos/%s/%s", org, repo)
	stdout, _, err := executeRESTCall(ctx, endpoint, false)
	if err != nil {
		return err
	}

	var apiResp struct {
		DefaultBranch            string `json:"default_branch"`
		Visibility               string `json:"visibility"`
		HasIssues                bool   `json:"has_issues"`
		HasProjects              bool   `json:"has_projects"`
		HasWiki                  bool   `json:"has_wiki"`
		HasDiscussions           bool   `json:"has_discussions"`
		AllowSquashMerge         bool   `json:"allow_squash_merge"`
		AllowMergeCommit         bool   `json:"allow_merge_commit"`
		AllowRebaseMerge         bool   `json:"allow_rebase_merge"`
		AllowAutoMerge           bool   `json:"allow_auto_merge"`
		DeleteBranchOnMerge      bool   `json:"delete_branch_on_merge"`
		AllowForking             bool   `json:"allow_forking"`
		WebCommitSignoffRequired bool   `json:"web_commit_signoff_required"`
	}

	if err := unmarshalJSON([]byte(stdout), &apiResp, "settings API response"); err != nil {
		return err
	}

	stats.Settings = &output.RepoSettings{
		DefaultBranch:            apiResp.DefaultBranch,
		Visibility:               apiResp.Visibility,
		HasIssues:                apiResp.HasIssues,
		HasProjects:              apiResp.HasProjects,
		HasWiki:                  apiResp.HasWiki,
		HasDiscussions:           apiResp.HasDiscussions,
		AllowSquashMerge:         apiResp.AllowSquashMerge,
		AllowMergeCommit:         apiResp.AllowMergeCommit,
		AllowRebaseMerge:         apiResp.AllowRebaseMerge,
		AllowAutoMerge:           apiResp.AllowAutoMerge,
		DeleteBranchOnMerge:      apiResp.DeleteBranchOnMerge,
		AllowForking:             apiResp.AllowForking,
		WebCommitSignoffRequired: apiResp.WebCommitSignoffRequired,
	}

	return nil
}

// fetchTeamAccess fetches team access to the repository.
func fetchTeamAccess(ctx context.Context, org, repo string, stats *output.RepoStats) error {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	var teams []output.RepoTeamAccess
	err := RunRESTCallback(ctx, fmt.Sprintf("/repos/%s/%s/teams", org, repo), func(data []byte) error {
		var page []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Slug       string `json:"slug"`
			Permission string `json:"permission"`
		}

		if err := json.Unmarshal(data, &page); err != nil {
			return err
		}

		for _, t := range page {
			teams = append(teams, output.RepoTeamAccess{
				TeamID:     t.ID,
				TeamName:   t.Name,
				TeamSlug:   t.Slug,
				Permission: t.Permission,
			})
		}

		return nil
	})

	if err != nil {
		return err
	}

	stats.TeamAccess = teams
	return nil
}

// fetchCustomPropertiesForRepo fetches custom property values for a repository.
// Uses centralized retry logic from executeRESTCall.
func fetchCustomPropertiesForRepo(ctx context.Context, org, repo string, stats *output.RepoStats) error {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	endpoint := fmt.Sprintf("/repos/%s/%s/properties/values", org, repo)
	stdout, stderr, err := executeRESTCall(ctx, endpoint, false)

	if err != nil {
		// 404 is expected if no custom properties
		if strings.Contains(stderr, "404") {
			return nil
		}
		return err
	}

	var properties []struct {
		PropertyName string      `json:"property_name"`
		Value        interface{} `json:"value"`
	}

	if err := unmarshalJSON([]byte(stdout), &properties, "custom properties API response"); err != nil {
		return err
	}

	if len(properties) > 0 {
		stats.CustomPropertiesRepo = make(map[string]interface{})
		for _, p := range properties {
			stats.CustomPropertiesRepo[p.PropertyName] = p.Value
		}
	}

	return nil
}

// fetchBranches fetches branches with protection rules.
func fetchBranches(ctx context.Context, org, repo string, stats *output.RepoStats) error {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	var branches []output.Branch
	err := RunRESTCallback(ctx, fmt.Sprintf("/repos/%s/%s/branches", org, repo), func(data []byte) error {
		var page []struct {
			Name      string `json:"name"`
			Protected bool   `json:"protected"`
		}

		if err := json.Unmarshal(data, &page); err != nil {
			return err
		}

		for _, b := range page {
			branch := output.Branch{
				Name:      b.Name,
				Protected: b.Protected,
			}

			// Fetch protection details if branch is protected
			if b.Protected {
				protection, err := fetchBranchProtection(ctx, org, repo, b.Name)
				if err == nil {
					branch.Protection = protection
				}
			}

			branches = append(branches, branch)
		}

		return nil
	})

	if err != nil {
		return err
	}

	// BranchesDetailed removed - using basic branch count from GraphQL instead
	return nil
}

// fetchBranchProtection fetches branch protection rules.
func fetchBranchProtection(ctx context.Context, org, repo, branch string) (*output.BranchProtection, error) {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	endpoint := fmt.Sprintf("/repos/%s/%s/branches/%s/protection", org, repo, branch)
	stdout, _, err := executeRESTCall(ctx, endpoint, false)
	if err != nil {
		// Not all protected branches have detailed protection rules
		return nil, err
	}

	var apiResp struct {
		RequiredStatusChecks *struct {
			Strict   bool     `json:"strict"`
			Contexts []string `json:"contexts"`
		} `json:"required_status_checks"`
		RequiredPullRequestReviews *struct {
			DismissStaleReviews          bool `json:"dismiss_stale_reviews"`
			RequireCodeOwnerReviews      bool `json:"require_code_owner_reviews"`
			RequiredApprovingReviewCount int  `json:"required_approving_review_count"`
		} `json:"required_pull_request_reviews"`
		RequiredSignatures *struct {
			Enabled bool `json:"enabled"`
		} `json:"required_signatures"`
		EnforceAdmins *struct {
			Enabled bool `json:"enabled"`
		} `json:"enforce_admins"`
		RequireLinearHistory *struct {
			Enabled bool `json:"enabled"`
		} `json:"required_linear_history"`
		AllowForcePushes *struct {
			Enabled bool `json:"enabled"`
		} `json:"allow_force_pushes"`
		AllowDeletions *struct {
			Enabled bool `json:"enabled"`
		} `json:"allow_deletions"`
		RequiredConversationResolution *struct {
			Enabled bool `json:"enabled"`
		} `json:"required_conversation_resolution"`
		LockBranch *struct {
			Enabled bool `json:"enabled"`
		} `json:"lock_branch"`
		AllowForkSyncing *struct {
			Enabled bool `json:"enabled"`
		} `json:"allow_fork_syncing"`
	}

	if err := unmarshalJSON([]byte(stdout), &apiResp, "branch protection API response"); err != nil {
		return nil, err
	}

	protection := &output.BranchProtection{}

	if apiResp.RequiredStatusChecks != nil {
		protection.RequiredStatusChecks = &struct {
			Strict   bool     `json:"strict"`
			Contexts []string `json:"contexts"`
		}{
			Strict:   apiResp.RequiredStatusChecks.Strict,
			Contexts: apiResp.RequiredStatusChecks.Contexts,
		}
	}

	if apiResp.RequiredPullRequestReviews != nil {
		protection.RequiredPullRequestReviews = &struct {
			DismissStaleReviews          bool `json:"dismissStaleReviews"`
			RequireCodeOwnerReviews      bool `json:"requireCodeOwnerReviews"`
			RequiredApprovingReviewCount int  `json:"requiredApprovingReviewCount"`
		}{
			DismissStaleReviews:          apiResp.RequiredPullRequestReviews.DismissStaleReviews,
			RequireCodeOwnerReviews:      apiResp.RequiredPullRequestReviews.RequireCodeOwnerReviews,
			RequiredApprovingReviewCount: apiResp.RequiredPullRequestReviews.RequiredApprovingReviewCount,
		}
	}

	if apiResp.RequiredSignatures != nil {
		protection.RequiredSignatures = apiResp.RequiredSignatures.Enabled
	}
	if apiResp.EnforceAdmins != nil {
		protection.EnforceAdmins = apiResp.EnforceAdmins.Enabled
	}
	if apiResp.RequireLinearHistory != nil {
		protection.RequireLinearHistory = apiResp.RequireLinearHistory.Enabled
	}
	if apiResp.AllowForcePushes != nil {
		protection.AllowForcePushes = apiResp.AllowForcePushes.Enabled
	}
	if apiResp.AllowDeletions != nil {
		protection.AllowDeletions = apiResp.AllowDeletions.Enabled
	}
	if apiResp.RequiredConversationResolution != nil {
		protection.RequiredConversationResolution = apiResp.RequiredConversationResolution.Enabled
	}
	if apiResp.LockBranch != nil {
		protection.LockBranch = apiResp.LockBranch.Enabled
	}
	if apiResp.AllowForkSyncing != nil {
		protection.AllowForkSyncing = apiResp.AllowForkSyncing.Enabled
	}

	return protection, nil
}

// fetchRepoWebhooks fetches repository webhooks.
func fetchRepoWebhooks(ctx context.Context, org, repo string, stats *output.RepoStats) error {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	var webhooks []output.Webhook
	err := RunRESTCallback(ctx, fmt.Sprintf("/repos/%s/%s/hooks", org, repo), func(data []byte) error {
		var page []output.Webhook

		if err := json.Unmarshal(data, &page); err != nil {
			return err
		}

		webhooks = append(webhooks, page...)
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to fetch webhooks: %w", err)
	}

	stats.Webhooks = webhooks
	return nil
}

// fetchAutolinks fetches autolink references for a repository.
func fetchAutolinks(ctx context.Context, org, repo string, stats *output.RepoStats) error {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	var autolinks []output.Autolink
	err := RunRESTCallback(ctx, fmt.Sprintf("/repos/%s/%s/autolinks", org, repo), func(data []byte) error {
		var page []struct {
			ID          int64  `json:"id"`
			KeyPrefix   string `json:"key_prefix"`
			URLTemplate string `json:"url_template"`
		}

		if err := json.Unmarshal(data, &page); err != nil {
			return err
		}

		for _, a := range page {
			autolinks = append(autolinks, output.Autolink{
				ID:          a.ID,
				KeyPrefix:   a.KeyPrefix,
				URLTemplate: a.URLTemplate,
			})
		}

		return nil
	})

	if err != nil {
		return err
	}

	stats.Autolinks = autolinks
	return nil
}

// fetchActionsData fetches Actions-related data (workflows, secrets, variables, runners, cache).
func fetchActionsData(ctx context.Context, org, repo string, stats *output.RepoStats) error {
	actions := &output.RepoActions{}

	// Fetch workflows
	if err := fetchWorkflows(ctx, org, repo, actions); err != nil {
		fmt.Printf("  WARNING Failed to fetch workflows for %s/%s: %v\n", org, repo, err)
	}

	// Fetch Actions secrets
	if err := fetchActionsSecretsRepo(ctx, org, repo, actions); err != nil {
		fmt.Printf("  WARNING Failed to fetch Actions secrets for %s/%s: %v\n", org, repo, err)
	}

	// Fetch Actions variables
	if err := fetchActionsVariablesRepo(ctx, org, repo, actions); err != nil {
		fmt.Printf("  WARNING Failed to fetch Actions variables for %s/%s: %v\n", org, repo, err)
	}

	// Fetch self-hosted runners
	if err := fetchRunnersRepo(ctx, org, repo, actions); err != nil {
		fmt.Printf("  WARNING Failed to fetch runners for %s/%s: %v\n", org, repo, err)
	}

	// Fetch cache usage (might not be available for all repos)
	_ = fetchCacheUsage(ctx, org, repo, actions)

	stats.Actions = actions
	return nil
}

// fetchWorkflows fetches GitHub Actions workflows.
func fetchWorkflows(ctx context.Context, org, repo string, actions *output.RepoActions) error {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	endpoint := fmt.Sprintf("/repos/%s/%s/actions/workflows", org, repo)
	stdout, _, err := executeRESTCall(ctx, endpoint, false)
	if err != nil {
		return err
	}

	var apiResp struct {
		Workflows []struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			Path      string `json:"path"`
			State     string `json:"state"`
			CreatedAt string `json:"created_at"`
			UpdatedAt string `json:"updated_at"`
		} `json:"workflows"`
	}

	if err := unmarshalJSON([]byte(stdout), &apiResp, "workflows API response"); err != nil {
		return err
	}

	// Set count instead of detailed workflows
	actions.WorkflowsCount = len(apiResp.Workflows)
	return nil
}

// fetchActionsSecretsRepo fetches Actions secrets for a repository.
func fetchActionsSecretsRepo(ctx context.Context, org, repo string, actions *output.RepoActions) error {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	endpoint := fmt.Sprintf("/repos/%s/%s/actions/secrets", org, repo)
	stdout, _, err := executeRESTCall(ctx, endpoint, false)
	if err != nil {
		return err
	}

	var apiResp struct {
		Secrets []output.SecretMetadata `json:"secrets"`
	}

	if err := unmarshalJSON([]byte(stdout), &apiResp, "secrets API response"); err != nil {
		return err
	}

	actions.Secrets = apiResp.Secrets
	actions.SecretsCount = len(apiResp.Secrets)
	return nil
}

// fetchActionsVariablesRepo fetches Actions variables for a repository.
func fetchActionsVariablesRepo(ctx context.Context, org, repo string, actions *output.RepoActions) error {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	endpoint := fmt.Sprintf("/repos/%s/%s/actions/variables", org, repo)
	stdout, _, err := executeRESTCall(ctx, endpoint, false)
	if err != nil {
		return err
	}

	var apiResp struct {
		Variables []output.Variable `json:"variables"`
	}

	if err := unmarshalJSON([]byte(stdout), &apiResp, "variables API response"); err != nil {
		return err
	}

	actions.Variables = apiResp.Variables
	actions.VariablesCount = len(apiResp.Variables)
	return nil
}

// fetchRunnersRepo fetches self-hosted runners for a repository.
func fetchRunnersRepo(ctx context.Context, org, repo string, actions *output.RepoActions) error {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	endpoint := fmt.Sprintf("/repos/%s/%s/actions/runners", org, repo)
	stdout, _, err := executeRESTCall(ctx, endpoint, false)
	if err != nil {
		return err
	}

	var apiResp struct {
		Runners []struct {
			ID int64 `json:"id"`
		} `json:"runners"`
	}

	if err := unmarshalJSON([]byte(stdout), &apiResp, "runners API response"); err != nil {
		return err
	}

	actions.RunnersCount = len(apiResp.Runners)
	return nil
}

// fetchCacheUsage fetches Actions cache usage statistics.
func fetchCacheUsage(ctx context.Context, org, repo string, actions *output.RepoActions) error {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	endpoint := fmt.Sprintf("/repos/%s/%s/actions/cache/usage", org, repo)
	stdout, _, err := executeRESTCall(ctx, endpoint, false)
	if err != nil {
		return err
	}

	var apiResp struct {
		ActiveCachesSizeInBytes int64 `json:"active_caches_size_in_bytes"`
		ActiveCachesCount       int   `json:"active_caches_count"`
	}

	if err := unmarshalJSON([]byte(stdout), &apiResp, "cache usage API response"); err != nil {
		return err
	}

	actions.CacheUsage = &output.CacheUsage{
		ActiveCachesSizeInBytes: apiResp.ActiveCachesSizeInBytes,
		ActiveCachesCount:       apiResp.ActiveCachesCount,
	}

	return nil
}

// fetchSecurityData fetches security-related data (Dependabot, code scanning, secret scanning, advisories).
func fetchSecurityData(ctx context.Context, org, repo string, stats *output.RepoStats) error {
	security := &output.RepoSecurity{}

	// Fetch Dependabot data
	// These security features might not be available for all repos
	_ = fetchDependabotData(ctx, org, repo, security)
	_ = fetchCodeScanningData(ctx, org, repo, security)
	_ = fetchSecretScanningData(ctx, org, repo, security)
	_ = fetchSecurityAdvisories(ctx, org, repo, security) // Advisories might not exist

	stats.Security = security
	return nil
}

// fetchDependabotData fetches Dependabot configuration and alerts.
func fetchDependabotData(ctx context.Context, org, repo string, security *output.RepoSecurity) error {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	endpoint := fmt.Sprintf("/repos/%s/%s/vulnerability-alerts", org, repo)
	_, _, err := executeRESTCall(ctx, endpoint, false)

	enabled := err == nil

	dependabot := &output.DependabotConfig{
		Enabled: enabled,
	}

	// Fetch Dependabot alerts if enabled
	if enabled {
		alertCounts, err := fetchDependabotAlerts(ctx, org, repo)
		if err == nil {
			dependabot.AlertCounts = alertCounts
		}

		// Fetch Dependabot secrets
		secrets, err := fetchDependabotSecrets(ctx, org, repo)
		if err == nil {
			dependabot.Secrets = secrets
		}
	}

	security.Dependabot = dependabot
	return nil
}

// fetchDependabotAlerts fetches Dependabot alert counts.
func fetchDependabotAlerts(ctx context.Context, org, repo string) (*output.AlertCounts, error) {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	var allAlerts []struct {
		State    string `json:"state"`
		Severity string `json:"security_advisory.severity"`
	}

	err := RunRESTCallback(ctx, fmt.Sprintf("/repos/%s/%s/dependabot/alerts", org, repo), func(data []byte) error {
		var page []struct {
			State            string `json:"state"`
			SecurityAdvisory struct {
				Severity string `json:"severity"`
			} `json:"security_advisory"`
		}

		if err := json.Unmarshal(data, &page); err != nil {
			return err
		}

		for _, alert := range page {
			allAlerts = append(allAlerts, struct {
				State    string `json:"state"`
				Severity string `json:"security_advisory.severity"`
			}{
				State:    alert.State,
				Severity: alert.SecurityAdvisory.Severity,
			})
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	counts := &output.AlertCounts{
		Open:   make(map[string]int),
		Closed: make(map[string]int),
	}

	for _, alert := range allAlerts {
		if alert.State == "open" {
			counts.Open[alert.Severity]++
		} else {
			counts.Closed[alert.Severity]++
		}
		counts.Total++
	}

	return counts, nil
}

// fetchDependabotSecrets fetches Dependabot secrets.
func fetchDependabotSecrets(ctx context.Context, org, repo string) ([]output.SecretMetadata, error) {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	endpoint := fmt.Sprintf("/repos/%s/%s/dependabot/secrets", org, repo)
	stdout, _, err := executeRESTCall(ctx, endpoint, false)
	if err != nil {
		return nil, err
	}

	var apiResp struct {
		Secrets []output.SecretMetadata `json:"secrets"`
	}

	if err := unmarshalJSON([]byte(stdout), &apiResp, "dependabot secrets API response"); err != nil {
		return nil, err
	}

	return apiResp.Secrets, nil
}

// fetchCodeScanningData fetches code scanning configuration and alerts.
func fetchCodeScanningData(ctx context.Context, org, repo string, security *output.RepoSecurity) error {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	// Fetch code scanning default setup
	endpoint := fmt.Sprintf("/repos/%s/%s/code-scanning/default-setup", org, repo)
	stdout, _, err := executeRESTCall(ctx, endpoint, false)

	codeScanning := &output.CodeScanningConfig{}

	if err == nil {
		var apiResp struct {
			State string `json:"state"`
		}

		if err := json.Unmarshal([]byte(stdout), &apiResp); err == nil {
			codeScanning.DefaultSetup = apiResp.State
		}
	}

	// Fetch code scanning alerts
	if alertCounts, err := fetchCodeScanningAlerts(ctx, org, repo); err == nil {
		codeScanning.AlertCounts = alertCounts
	}

	security.CodeScanning = codeScanning
	return nil
}

// fetchCodeScanningAlerts fetches code scanning alert counts.
func fetchCodeScanningAlerts(ctx context.Context, org, repo string) (*output.AlertCounts, error) {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	var allAlerts []struct {
		State string `json:"state"`
		Rule  struct {
			SecuritySeverityLevel string `json:"security_severity_level"`
		} `json:"rule"`
	}

	err := RunRESTCallback(ctx, fmt.Sprintf("/repos/%s/%s/code-scanning/alerts", org, repo), func(data []byte) error {
		var page []struct {
			State string `json:"state"`
			Rule  struct {
				SecuritySeverityLevel string `json:"security_severity_level"`
			} `json:"rule"`
		}

		if err := json.Unmarshal(data, &page); err != nil {
			return err
		}

		allAlerts = append(allAlerts, page...)
		return nil
	})

	if err != nil {
		return nil, err
	}

	counts := &output.AlertCounts{
		Open:   make(map[string]int),
		Closed: make(map[string]int),
	}

	for _, alert := range allAlerts {
		severity := alert.Rule.SecuritySeverityLevel
		if severity == "" {
			severity = "unknown"
		}

		if alert.State == "open" {
			counts.Open[severity]++
		} else {
			counts.Closed[severity]++
		}
		counts.Total++
	}

	return counts, nil
}

// fetchSecretScanningData fetches secret scanning configuration and alerts.
func fetchSecretScanningData(ctx context.Context, org, repo string, security *output.RepoSecurity) error {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	// Check if secret scanning is enabled by trying to fetch alerts
	var allAlerts []struct {
		State string `json:"state"`
	}

	err := RunRESTCallback(ctx, fmt.Sprintf("/repos/%s/%s/secret-scanning/alerts", org, repo), func(data []byte) error {
		var page []struct {
			State string `json:"state"`
		}

		if err := json.Unmarshal(data, &page); err != nil {
			return err
		}

		allAlerts = append(allAlerts, page...)
		return nil
	})

	secretScanning := &output.SecretScanningConfig{
		Enabled: err == nil,
	}

	if err == nil {
		counts := &output.AlertCounts{
			Open:   make(map[string]int),
			Closed: make(map[string]int),
		}

		for _, alert := range allAlerts {
			if alert.State == "open" {
				counts.Open["total"]++
			} else {
				counts.Closed["total"]++
			}
			counts.Total++
		}

		secretScanning.AlertCounts = counts
	}

	security.SecretScanning = secretScanning
	return nil
}

// fetchSecurityAdvisories fetches repository security advisories.
func fetchSecurityAdvisories(ctx context.Context, org, repo string, security *output.RepoSecurity) error {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	var advisories []output.SecurityAdvisory

	err := RunRESTCallback(ctx, fmt.Sprintf("/repos/%s/%s/security-advisories", org, repo), func(data []byte) error {
		var page []struct {
			GHSAID      string `json:"ghsa_id"`
			Summary     string `json:"summary"`
			Description string `json:"description"`
			Severity    string `json:"severity"`
			State       string `json:"state"`
			PublishedAt string `json:"published_at"`
			UpdatedAt   string `json:"updated_at"`
		}

		if err := json.Unmarshal(data, &page); err != nil {
			return err
		}

		for _, adv := range page {
			advisories = append(advisories, output.SecurityAdvisory{
				ID:          adv.GHSAID,
				Summary:     adv.Summary,
				Description: adv.Description,
				Severity:    adv.Severity,
				State:       adv.State,
				PublishedAt: adv.PublishedAt,
				UpdatedAt:   adv.UpdatedAt,
			})
		}

		return nil
	})

	if err != nil {
		// Advisories might not exist for most repos
		return nil
	}

	security.SecurityAdvisories = advisories
	return nil
}

// fetchPages fetches GitHub Pages configuration.
func fetchPages(ctx context.Context, org, repo string, stats *output.RepoStats) error {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	endpoint := fmt.Sprintf("/repos/%s/%s/pages", org, repo)
	stdout, stderr, err := executeRESTCall(ctx, endpoint, false)

	if err != nil {
		// 404 is expected if no Pages
		if strings.Contains(stderr, "404") {
			return nil
		}
		return err
	}

	var apiResp struct {
		URL       string `json:"url"`
		Status    string `json:"status"`
		CNAME     string `json:"cname"`
		Custom404 bool   `json:"custom_404"`
		Source    struct {
			Branch string `json:"branch"`
			Path   string `json:"path"`
		} `json:"source"`
		Public           bool `json:"public"`
		HTTPSEnforced    bool `json:"https_enforced"`
		HTTPSCertificate struct {
			State   string   `json:"state"`
			Domains []string `json:"domains"`
		} `json:"https_certificate"`
	}

	if err := unmarshalJSON([]byte(stdout), &apiResp, "pages API response"); err != nil {
		return err
	}

	stats.Pages = &output.PagesConfig{
		URL:       apiResp.URL,
		Status:    apiResp.Status,
		CNAME:     apiResp.CNAME,
		Custom404: apiResp.Custom404,
		HTMLSource: &struct {
			Branch string `json:"branch"`
			Path   string `json:"path"`
		}{
			Branch: apiResp.Source.Branch,
			Path:   apiResp.Source.Path,
		},
		Public:        apiResp.Public,
		HTTPSEnforced: apiResp.HTTPSEnforced,
		HTTPSCertificate: &struct {
			State   string   `json:"state"`
			Domains []string `json:"domains"`
		}{
			State:   apiResp.HTTPSCertificate.State,
			Domains: apiResp.HTTPSCertificate.Domains,
		},
	}

	return nil
}

// ============================================================================
// Phase 3: Issues & Pull Requests Data Collection.
// ============================================================================

// fetchIssuesData fetches comprehensive issue statistics including labels and milestones.
func fetchIssuesData(ctx context.Context, org, repo string, stats *output.RepoStats) error {
	issuesData := &output.IssuesData{}

	// Fetch open and closed issue counts
	if err := fetchIssueCounts(ctx, org, repo, issuesData); err != nil {
		return err
	}

	// Fetch labels count
	if err := fetchLabelsCount(ctx, org, repo, issuesData); err != nil {
		fmt.Printf("  WARNING Failed to fetch labels count for %s/%s: %v\n", org, repo, err)
	}

	// Fetch all milestones
	if err := fetchMilestones(ctx, org, repo, issuesData); err != nil {
		fmt.Printf("  WARNING Failed to fetch milestones for %s/%s: %v\n", org, repo, err)
	}

	stats.IssuesData = issuesData
	return nil
}

// fetchIssueCounts fetches open and closed issue counts.
func fetchIssueCounts(ctx context.Context, org, repo string, issuesData *output.IssuesData) error {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	// Fetch issue counts using search API for efficiency
	// Get open issues
	endpoint := fmt.Sprintf("/repos/%s/%s/issues?state=open&per_page=1", org, repo)
	stdout, _, err := executeRESTCall(ctx, endpoint, false)

	if err == nil {
		var openIssues []map[string]interface{}
		if err := json.Unmarshal([]byte(stdout), &openIssues); err == nil {
			// Get the total count from a separate call
			issuesData.OpenCount = countIssues(ctx, org, repo, "open")
		}
	}

	// Get closed issues count
	issuesData.ClosedCount = countIssues(ctx, org, repo, "closed")
	issuesData.TotalCount = issuesData.OpenCount + issuesData.ClosedCount

	return nil
}

// countIssues counts issues for a given state.
func countIssues(ctx context.Context, org, repo, issueState string) int {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	count := 0
	err := RunRESTCallback(ctx, fmt.Sprintf("/repos/%s/%s/issues?state=%s&per_page=100", org, repo, issueState), func(data []byte) error {
		var page []map[string]interface{}
		if err := json.Unmarshal(data, &page); err != nil {
			return err
		}
		// Filter out pull requests (issues endpoint returns both)
		for _, item := range page {
			if _, isPR := item["pull_request"]; !isPR {
				count++
			}
		}
		return nil
	})

	if err != nil {
		return 0
	}

	return count
}

// fetchLabelsCount fetches the count of repository labels.
func fetchLabelsCount(ctx context.Context, org, repo string, issuesData *output.IssuesData) error {
	endpoint := fmt.Sprintf("/repos/%s/%s/labels", org, repo)

	count, err := getCountFromLinkHeader(ctx, endpoint)
	if err != nil {
		return err
	}

	issuesData.LabelsCount = count
	return nil
}

// fetchMilestones fetches all repository milestones.
func fetchMilestones(ctx context.Context, org, repo string, issuesData *output.IssuesData) error {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	var milestones []output.Milestone
	// Fetch both open and closed milestones
	for _, milestoneState := range []string{"open", "closed"} {
		err := RunRESTCallback(ctx, fmt.Sprintf("/repos/%s/%s/milestones?state=%s", org, repo, milestoneState), func(data []byte) error {
			var page []struct {
				Number       int    `json:"number"`
				Title        string `json:"title"`
				Description  string `json:"description"`
				State        string `json:"state"`
				OpenIssues   int    `json:"open_issues"`
				ClosedIssues int    `json:"closed_issues"`
				DueOn        string `json:"due_on"`
				CreatedAt    string `json:"created_at"`
				UpdatedAt    string `json:"updated_at"`
				ClosedAt     string `json:"closed_at"`
			}

			if err := json.Unmarshal(data, &page); err != nil {
				return err
			}

			for _, m := range page {
				milestones = append(milestones, output.Milestone{
					Number:       m.Number,
					Title:        m.Title,
					Description:  m.Description,
					State:        m.State,
					OpenIssues:   m.OpenIssues,
					ClosedIssues: m.ClosedIssues,
					DueOn:        m.DueOn,
					CreatedAt:    m.CreatedAt,
					UpdatedAt:    m.UpdatedAt,
					ClosedAt:     m.ClosedAt,
				})
			}

			return nil
		})

		if err != nil {
			return err
		}
	}

	issuesData.Milestones = milestones
	issuesData.MilestonesCount = len(milestones)
	return nil
}

// fetchPullRequestsData fetches comprehensive pull request statistics.
func fetchPullRequestsData(ctx context.Context, org, repo string, stats *output.RepoStats) error {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	prData := &output.PullRequestsData{}

	// Count open PRs
	prData.OpenCount = countPullRequests(ctx, org, repo, "open")

	// Count closed PRs and calculate merge statistics
	closedCount, mergedCount, avgHours := countAndAnalyzePullRequests(ctx, org, repo)
	prData.ClosedCount = closedCount
	prData.MergedCount = mergedCount
	prData.TotalCount = prData.OpenCount + prData.ClosedCount

	if avgHours > 0 {
		prData.AvgTimeToMergeHours = avgHours
		prData.AvgTimeToMerge = formatDuration(avgHours)
	}

	stats.PullRequestsData = prData
	return nil
}

// countPullRequests counts pull requests for a given state.
func countPullRequests(ctx context.Context, org, repo, prState string) int {
	endpoint := fmt.Sprintf("/repos/%s/%s/pulls?state=%s", org, repo, prState)

	count, err := getCountFromLinkHeader(ctx, endpoint)
	if err != nil {
		return 0
	}

	return count
}

// countAndAnalyzePullRequests counts closed/merged PRs and calculates average time to merge.
func countAndAnalyzePullRequests(ctx context.Context, org, repo string) (closedCount, mergedCount int, avgTimeToMergeHours float64) {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	var totalMergeTime float64
	var mergedPRs int

	// Sample recent closed PRs (up to 300) to calculate average merge time
	pageCount := 0
	maxPages := 3

	err := RunRESTCallback(ctx, fmt.Sprintf("/repos/%s/%s/pulls?state=closed&per_page=100", org, repo), func(data []byte) error {
		if pageCount >= maxPages {
			return nil
		}
		pageCount++

		var page []struct {
			CreatedAt string      `json:"created_at"`
			MergedAt  interface{} `json:"merged_at"`
			ClosedAt  string      `json:"closed_at"`
		}

		if err := json.Unmarshal(data, &page); err != nil {
			return err
		}

		closedCount += len(page)

		for _, pr := range page {
			if pr.MergedAt != nil {
				mergedCount++
				mergedPRs++

				// Calculate time to merge
				mergedAtStr, ok := pr.MergedAt.(string)
				if ok && mergedAtStr != "" {
					createdAt, err1 := parseTime(pr.CreatedAt)
					mergedAt, err2 := parseTime(mergedAtStr)

					if err1 == nil && err2 == nil {
						duration := mergedAt.Sub(createdAt)
						totalMergeTime += duration.Hours()
					}
				}
			}
		}

		return nil
	})

	if err == nil && mergedPRs > 0 {
		avgTimeToMergeHours = totalMergeTime / float64(mergedPRs)
	}

	return closedCount, mergedCount, avgTimeToMergeHours
}

// parseTime parses ISO 8601 time strings.
func parseTime(timeStr string) (time.Time, error) {
	return time.Parse(time.RFC3339, timeStr)
}

// formatDuration formats hours into a human-readable duration.
func formatDuration(hours float64) string {
	if hours < 1 {
		return fmt.Sprintf("%.1f hours", hours)
	} else if hours < 24 {
		return fmt.Sprintf("%.1f hours", hours)
	} else {
		days := hours / 24
		if days < 7 {
			return fmt.Sprintf("%.1f days", days)
		} else if days < 30 {
			weeks := days / 7
			return fmt.Sprintf("%.1f weeks", weeks)
		} else {
			months := days / 30
			return fmt.Sprintf("%.1f months", months)
		}
	}
}

// ============================================================================
// Phase 4: Additional Data Points Collection.
// ============================================================================

// fetchTrafficData fetches repository traffic data (views and clones).
func fetchTrafficData(ctx context.Context, org, repo string, stats *output.RepoStats) error {
	trafficData := &output.TrafficData{}

	// Fetch views (requires push access - expected to fail for many repos)
	if views, err := fetchTrafficViews(ctx, org, repo); err == nil {
		trafficData.Views = views
	}

	// Fetch clones
	if clones, err := fetchTrafficClones(ctx, org, repo); err == nil {
		trafficData.Clones = clones
	}

	// Only set if we got at least some data
	if trafficData.Views != nil || trafficData.Clones != nil {
		stats.Traffic = trafficData
	}

	return nil
}

// fetchTrafficViews fetches repository view statistics.
func fetchTrafficViews(ctx context.Context, org, repo string) (*output.TrafficStats, error) {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	endpoint := fmt.Sprintf("/repos/%s/%s/traffic/views", org, repo)
	stdout, _, err := executeRESTCall(ctx, endpoint, false)
	if err != nil {
		return nil, err
	}

	var apiResp struct {
		Count   int `json:"count"`
		Uniques int `json:"uniques"`
		Views   []struct {
			Timestamp string `json:"timestamp"`
			Count     int    `json:"count"`
			Uniques   int    `json:"uniques"`
		} `json:"views"`
	}

	if err := unmarshalJSON([]byte(stdout), &apiResp, "traffic views API response"); err != nil {
		return nil, err
	}

	trafficStats := &output.TrafficStats{
		Count:   apiResp.Count,
		Uniques: apiResp.Uniques,
	}

	for _, v := range apiResp.Views {
		trafficStats.Details = append(trafficStats.Details, output.TrafficDetail{
			Timestamp: v.Timestamp,
			Count:     v.Count,
			Uniques:   v.Uniques,
		})
	}

	return trafficStats, nil
}

// fetchTrafficClones fetches repository clone statistics.
func fetchTrafficClones(ctx context.Context, org, repo string) (*output.TrafficStats, error) {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	endpoint := fmt.Sprintf("/repos/%s/%s/traffic/clones", org, repo)
	stdout, _, err := executeRESTCall(ctx, endpoint, false)
	if err != nil {
		return nil, err
	}

	var apiResp struct {
		Count   int `json:"count"`
		Uniques int `json:"uniques"`
		Clones  []struct {
			Timestamp string `json:"timestamp"`
			Count     int    `json:"count"`
			Uniques   int    `json:"uniques"`
		} `json:"clones"`
	}

	if err := unmarshalJSON([]byte(stdout), &apiResp, "traffic clones API response"); err != nil {
		return nil, err
	}

	trafficStats := &output.TrafficStats{
		Count:   apiResp.Count,
		Uniques: apiResp.Uniques,
	}

	for _, c := range apiResp.Clones {
		trafficStats.Details = append(trafficStats.Details, output.TrafficDetail{
			Timestamp: c.Timestamp,
			Count:     c.Count,
			Uniques:   c.Uniques,
		})
	}

	return trafficStats, nil
}

// ============================================================================
// Phase 5: Deployment & Git Metadata Collection.
// ============================================================================

// fetchTagsDetailed fetches detailed tag information.
func fetchTagsDetailed(ctx context.Context, org, repo string, stats *output.RepoStats) error {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	var tags []output.GitTag

	err := RunRESTCallback(ctx, fmt.Sprintf("/repos/%s/%s/tags", org, repo), func(data []byte) error {
		var page []struct {
			Name       string `json:"name"`
			ZipballURL string `json:"zipball_url"`
			TarballURL string `json:"tarball_url"`
			Commit     struct {
				SHA string `json:"sha"`
				URL string `json:"url"`
			} `json:"commit"`
			NodeID string `json:"node_id"`
		}

		if err := json.Unmarshal(data, &page); err != nil {
			return err
		}

		for _, t := range page {
			tag := output.GitTag{
				Name:       t.Name,
				SHA:        t.Commit.SHA,
				ZipballURL: t.ZipballURL,
				TarballURL: t.TarballURL,
				NodeID:     t.NodeID,
				Commit: &output.TagCommit{
					SHA: t.Commit.SHA,
					URL: t.Commit.URL,
				},
			}
			tags = append(tags, tag)
		}

		return nil
	})

	if err != nil {
		return err
	}

	// TagsDetailed removed - using basic Tags count from GraphQL instead
	return nil
}

// fetchGitReferences counts all git references (branches, tags, pull requests).
func fetchGitReferences(ctx context.Context, org, repo string, stats *output.RepoStats) error {
	endpoint := fmt.Sprintf("/repos/%s/%s/git/refs", org, repo)

	count, err := getCountFromLinkHeader(ctx, endpoint)
	if err != nil {
		return err
	}

	stats.GitReferencesCount = count
	return nil
}

// fetchGitLFSStatus checks if Git LFS is enabled for the repository.
func fetchGitLFSStatus(ctx context.Context, org, repo string, stats *output.RepoStats) error {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	endpoint := fmt.Sprintf("/repos/%s/%s/lfs", org, repo)
	stdout, _, err := executeRESTCall(ctx, endpoint, false)

	if err != nil {
		// If endpoint doesn't exist or returns error, assume LFS is not enabled
		enabled := false
		stats.GitLFSEnabled = &enabled
		return nil
	}

	var apiResp struct {
		Enabled bool `json:"enabled"`
	}

	if err := unmarshalJSON([]byte(stdout), &apiResp, "LFS API response"); err != nil {
		// If we can't parse, assume not enabled
		enabled := false
		stats.GitLFSEnabled = &enabled
		return nil
	}

	stats.GitLFSEnabled = &apiResp.Enabled
	return nil
}

// fetchRepoFiles fetches important repository files (README, CODEOWNERS).
func fetchRepoFiles(ctx context.Context, org, repo string, stats *output.RepoStats) error {
	repoFiles := &output.RepoFiles{}

	// Fetch README
	if readme, err := fetchFileContent(ctx, org, repo, "README.md"); err == nil {
		repoFiles.Readme = readme
	} else if readme, err := fetchFileContent(ctx, org, repo, "README"); err == nil {
		repoFiles.Readme = readme
	} else if readme, err := fetchFileContent(ctx, org, repo, "readme.md"); err == nil {
		repoFiles.Readme = readme
	}

	// Fetch CODEOWNERS
	if codeowners, err := fetchFileContent(ctx, org, repo, ".github/CODEOWNERS"); err == nil {
		repoFiles.CodeOwners = codeowners
	} else if codeowners, err := fetchFileContent(ctx, org, repo, "CODEOWNERS"); err == nil {
		repoFiles.CodeOwners = codeowners
	} else if codeowners, err := fetchFileContent(ctx, org, repo, "docs/CODEOWNERS"); err == nil {
		repoFiles.CodeOwners = codeowners
	}

	// Only set if we found at least one file
	if repoFiles.Readme != nil || repoFiles.CodeOwners != nil {
		stats.RepoFiles = repoFiles
	}

	return nil
}

// fetchFileContent fetches file metadata (not content to keep data size reasonable).
func fetchFileContent(ctx context.Context, org, repo, path string) (*output.RepoFileContent, error) {
	_ = state.Get().CheckRateLimit(1) // Error handled internally

	endpoint := fmt.Sprintf("/repos/%s/%s/contents/%s", org, repo, path)
	stdout, _, err := executeRESTCall(ctx, endpoint, false)
	if err != nil {
		return nil, err
	}

	var apiResp struct {
		Name        string `json:"name"`
		Path        string `json:"path"`
		SHA         string `json:"sha"`
		Size        int    `json:"size"`
		URL         string `json:"url"`
		HTMLURL     string `json:"html_url"`
		DownloadURL string `json:"download_url"`
		Type        string `json:"type"`
		// Omit content field to avoid storing base64 encoded file content
	}

	if err := unmarshalJSON([]byte(stdout), &apiResp, "file content API response"); err != nil {
		return nil, err
	}

	return &output.RepoFileContent{
		Name:        apiResp.Name,
		Path:        apiResp.Path,
		SHA:         apiResp.SHA,
		Size:        apiResp.Size,
		URL:         apiResp.URL,
		HTMLURL:     apiResp.HTMLURL,
		DownloadURL: apiResp.DownloadURL,
		Type:        apiResp.Type,
	}, nil
}

// ============================================================================
// Phase 6: Additional Engagement Metrics.
// ============================================================================

// fetchContributorsCount fetches the count of contributors to a repository.
// Uses Link header parsing for fast count retrieval (1 API call instead of N pages).
func fetchContributorsCount(ctx context.Context, org, repo string, stats *output.RepoStats) error {
	endpoint := fmt.Sprintf("/repos/%s/%s/contributors?anon=true", org, repo)

	count, err := getCountFromLinkHeader(ctx, endpoint)
	if err != nil {
		return fmt.Errorf("failed to get contributors count: %w", err)
	}

	stats.Contributors = count
	return nil
}

// fetchCommitCount fetches the total commit count for a repository.
// Uses Link header parsing for fast and accurate count retrieval (1 API call).
func fetchCommitCount(ctx context.Context, org, repo string, stats *output.RepoStats) error {
	endpoint := fmt.Sprintf("/repos/%s/%s/commits", org, repo)

	count, err := getCountFromLinkHeader(ctx, endpoint)
	if err != nil {
		return fmt.Errorf("failed to get commit count: %w", err)
	}

	stats.Commits = count
	return nil
}
