// Package output provides JSON output functionality for repository statistics and package data.
package output

import (
	"os"
)

// OrgMetadata holds comprehensive metadata about a GitHub organization.
type OrgMetadata struct {
	// Basic information
	Login       string `json:"login"`
	ID          int64  `json:"id"`
	NodeID      string `json:"nodeId"`
	Name        string `json:"name,omitempty"`
	Company     string `json:"company,omitempty"`
	Blog        string `json:"blog,omitempty"`
	Location    string `json:"location,omitempty"`
	Email       string `json:"email,omitempty"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url"`

	// Social
	TwitterUsername string `json:"twitterUsername,omitempty"`

	// Statistics
	PublicRepos       int `json:"publicRepos"`
	PublicGists       int `json:"publicGists"`
	Followers         int `json:"followers"`
	Following         int `json:"following"`
	TotalPrivateRepos int `json:"totalPrivateRepos,omitempty"`
	OwnedPrivateRepos int `json:"ownedPrivateRepos,omitempty"`

	// Settings
	DefaultRepoPermission         string `json:"defaultRepoPermission,omitempty"`
	TwoFactorRequirementEnabled   bool   `json:"twoFactorRequirementEnabled,omitempty"`
	MembersCanCreateRepos         bool   `json:"membersCanCreateRepos,omitempty"`
	MembersCanCreatePublicRepos   bool   `json:"membersCanCreatePublicRepos,omitempty"`
	MembersCanCreatePrivateRepos  bool   `json:"membersCanCreatePrivateRepos,omitempty"`
	MembersCanCreateInternalRepos bool   `json:"membersCanCreateInternalRepos,omitempty"`
	MembersCanForkPrivateRepos    bool   `json:"membersCanForkPrivateRepos,omitempty"`
	WebCommitSignoffRequired      bool   `json:"webCommitSignoffRequired,omitempty"`

	// Timestamps
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`

	// Counts (aggregates)
	MembersCount              int `json:"membersCount"`
	OutsideCollaboratorsCount int `json:"outsideCollaboratorsCount"`
	TeamsCount                int `json:"teamsCount"`
	RunnersCount              int `json:"runnersCount"`
	OrganizationRolesCount    int `json:"organizationRolesCount"`
	GitHubAppsCount           int `json:"githubAppsCount"`   // Number of GitHub App installations
	OrgPackagesCount          int `json:"orgPackagesCount"`  // Number of packages at org level
	TotalDeployKeysCount      int `json:"totalDeployKeys"`   // Total deploy keys across all repos
	TotalContributorsCount    int `json:"totalContributors"` // Unique contributors across org

	// Detailed data (Phase 1 additions - kept for migration relevance)
	SecurityManagers []SecurityManager `json:"securityManagers,omitempty"`
	CustomProperties []CustomProperty  `json:"customProperties,omitempty"`
	Webhooks         []Webhook         `json:"webhooks,omitempty"`
	ActionsSecrets   []SecretMetadata  `json:"actionsSecrets,omitempty"`
	ActionsVariables []Variable        `json:"actionsVariables,omitempty"`
	Rulesets         []Ruleset         `json:"rulesets,omitempty"`
	BlockedUsers     []BlockedUser     `json:"blockedUsers,omitempty"`
}

// SecurityManager represents a team designated as a security manager.
type SecurityManager struct {
	TeamID   int64  `json:"teamId"`
	TeamName string `json:"teamName"`
	TeamSlug string `json:"teamSlug"`
}

// CustomProperty represents a custom property definition for repositories.
type CustomProperty struct {
	PropertyName  string   `json:"propertyName"`
	ValueType     string   `json:"valueType"` // string, single_select, multi_select, true_false
	Required      bool     `json:"required"`
	DefaultValue  string   `json:"defaultValue,omitempty"`
	Description   string   `json:"description,omitempty"`
	AllowedValues []string `json:"allowedValues,omitempty"` // For select types
}

// OrganizationRole represents a custom organization role.
// OrgMember represents an organization member with their role.

// Webhook represents an organization or repository webhook.
type Webhook struct {
	ID     int64    `json:"id"`
	Name   string   `json:"name"`
	Active bool     `json:"active"`
	Events []string `json:"events"`
	Config struct {
		URL         string `json:"url"`
		ContentType string `json:"contentType"`
		InsecureSSL string `json:"insecureSsl"`
	} `json:"config"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// SecretMetadata represents metadata about a secret (not the value).
type SecretMetadata struct {
	Name       string `json:"name"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
	Visibility string `json:"visibility,omitempty"` // all, private, selected
}

// Variable represents an Actions or Dependabot variable.
type Variable struct {
	Name       string `json:"name"`
	Value      string `json:"value"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
	Visibility string `json:"visibility,omitempty"` // all, private, selected
}

// Ruleset represents an organization or repository ruleset.
type Ruleset struct {
	ID           int64         `json:"id"`
	Name         string        `json:"name"`
	Target       string        `json:"target,omitempty"`     // branch, tag, push
	SourceType   string        `json:"sourceType,omitempty"` // Repository, Organization
	Source       string        `json:"source"`
	Enforcement  string        `json:"enforcement"` // disabled, active, evaluate
	BypassActors []BypassActor `json:"bypassActors,omitempty"`
	Conditions   interface{}   `json:"conditions,omitempty"` // Complex structure, keep as interface{}
	Rules        []Rule        `json:"rules,omitempty"`
	CreatedAt    string        `json:"createdAt"`
	UpdatedAt    string        `json:"updatedAt"`
}

// BypassActor represents an actor who can bypass a ruleset.
type BypassActor struct {
	ActorID    int64  `json:"actorId"`
	ActorType  string `json:"actorType"`  // Team, Integration, OrganizationAdmin
	BypassMode string `json:"bypassMode"` // always, pull_request
}

// Rule represents a rule within a ruleset.
type Rule struct {
	Type       string      `json:"type"`
	Parameters interface{} `json:"parameters,omitempty"` // Varies by rule type
}

// BlockedUser represents a user blocked by the organization.
type BlockedUser struct {
	Login     string `json:"login"`
	ID        int64  `json:"id"`
	NodeID    string `json:"nodeId"`
	AvatarURL string `json:"avatarUrl,omitempty"`
	Type      string `json:"type"` // User, Bot
	SiteAdmin bool   `json:"siteAdmin"`
	BlockedAt string `json:"blockedAt,omitempty"`
}

// ConsolidatedStats holds all statistics in a consolidated format with orgs, repos, and packages.
type ConsolidatedStats struct {
	Orgs     []OrgMetadata `json:"orgs"`
	Repos    []RepoStats   `json:"repos"`
	Packages []PackageData `json:"packages"`
}

// RepoStats holds all statistics for a single repository.
// Includes comprehensive migration data from Phase 2.
type RepoStats struct {
	// Basic metadata
	Org        string `json:"org"`
	Name       string `json:"repo"`
	URL        string `json:"url"`
	IsFork     bool   `json:"isFork"`
	IsArchived bool   `json:"isArchived"`
	SizeMB     int    `json:"sizeMB"`
	HasWiki    bool   `json:"hasWiki"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
	PushedAt   string `json:"pushedAt"`

	// Engagement metrics
	Stars    int `json:"stars"`
	Watchers int `json:"watchers"`
	Forks    int `json:"forks"`

	// Basic counts
	Collaborators     int `json:"collaborators"`
	Contributors      int `json:"contributors"` // Total number of contributors
	Branches          int `json:"branches"`
	Tags              int `json:"tags"`
	ProtectedBranches int `json:"protectedBranches"`
	Issues            int `json:"issues"`
	OpenIssues        int `json:"openIssues"`   // Open issues count
	ClosedIssues      int `json:"closedIssues"` // Closed issues count
	PRs               int `json:"pullRequests"`
	OpenPRs           int `json:"openPullRequests"`   // Open PRs count
	ClosedPRs         int `json:"closedPullRequests"` // Closed PRs count
	MergedPRs         int `json:"mergedPullRequests"` // Merged PRs count
	Commits           int `json:"commits"`            // Total commit count
	Milestones        int `json:"milestones"`
	Releases          int `json:"releases"`
	Projects          int `json:"projects"`
	Discussions       int `json:"discussions"`
	CommitComments    int `json:"commitComments"`
	IssueEvents       int `json:"issueEvents"` // Number of issue events
	Packages          int `json:"packages"`
	PackageVersions   int `json:"packageVersions"` // Total sum of package version counts for this repository

	// Phase 2: Detailed repository data (only populated for JSON format)
	Settings              *RepoSettings          `json:"settings,omitempty"`
	CollaboratorsDetailed []RepoCollaborator     `json:"collaboratorsDetailed,omitempty"`
	TeamAccess            []RepoTeamAccess       `json:"teamAccess,omitempty"`
	Rulesets              []Ruleset              `json:"rulesets,omitempty"`
	Webhooks              []Webhook              `json:"webhooks,omitempty"`
	DeployKeys            []DeployKey            `json:"deployKeys,omitempty"`
	Autolinks             []Autolink             `json:"autolinks,omitempty"`
	Actions               *RepoActions           `json:"actions,omitempty"`
	Environments          []Environment          `json:"environments,omitempty"`
	Security              *RepoSecurity          `json:"security,omitempty"`
	Topics                []string               `json:"topics,omitempty"`
	Languages             map[string]int         `json:"languages,omitempty"`
	License               *License               `json:"license,omitempty"`
	Pages                 *PagesConfig           `json:"pages,omitempty"`
	CustomPropertiesRepo  map[string]interface{} `json:"customProperties,omitempty"`

	// Phase 3: Issues & Pull Requests (summary data only)
	IssuesData       *IssuesData       `json:"issuesData,omitempty"`
	PullRequestsData *PullRequestsData `json:"pullRequestsData,omitempty"`

	// Phase 4: Additional Data Points
	Traffic          *TrafficData      `json:"traffic,omitempty"`
	CommunityProfile *CommunityProfile `json:"communityProfile,omitempty"`

	// Phase 5: Deployment & Git Metadata
	Deployments        []Deployment `json:"deployments,omitempty"`
	GitReferencesCount int          `json:"gitReferencesCount"`
	GitLFSEnabled      *bool        `json:"gitLFSEnabled,omitempty"`
	RepoFiles          *RepoFiles   `json:"repoFiles,omitempty"`
}

// RepoSettings holds repository settings and configurations.
type RepoSettings struct {
	DefaultBranch            string `json:"defaultBranch"`
	Visibility               string `json:"visibility"` // public, private, internal
	HasIssues                bool   `json:"hasIssues"`
	HasProjects              bool   `json:"hasProjects"`
	HasWiki                  bool   `json:"hasWiki"`
	HasDiscussions           bool   `json:"hasDiscussions"`
	AllowSquashMerge         bool   `json:"allowSquashMerge"`
	AllowMergeCommit         bool   `json:"allowMergeCommit"`
	AllowRebaseMerge         bool   `json:"allowRebaseMerge"`
	AllowAutoMerge           bool   `json:"allowAutoMerge"`
	DeleteBranchOnMerge      bool   `json:"deleteBranchOnMerge"`
	AllowForking             bool   `json:"allowForking"`
	WebCommitSignoffRequired bool   `json:"webCommitSignoffRequired"`
}

// RepoCollaborator represents a repository collaborator with permissions.
type RepoCollaborator struct {
	Login       string `json:"login"`
	ID          int64  `json:"id"`
	Permissions struct {
		Admin    bool `json:"admin"`
		Maintain bool `json:"maintain"`
		Push     bool `json:"push"`
		Triage   bool `json:"triage"`
		Pull     bool `json:"pull"`
	} `json:"permissions"`
	RoleName string `json:"roleName,omitempty"` // Custom role name if applicable
}

// RepoTeamAccess represents a team's access to a repository.
type RepoTeamAccess struct {
	TeamID     int64  `json:"teamId"`
	TeamName   string `json:"teamName"`
	TeamSlug   string `json:"teamSlug"`
	Permission string `json:"permission"` // pull, push, admin, maintain, triage
}

// Branch represents a branch with its protection rules.
type Branch struct {
	Name       string            `json:"name"`
	Protected  bool              `json:"protected"`
	Protection *BranchProtection `json:"protection,omitempty"`
}

// BranchProtection represents branch protection rules.
type BranchProtection struct {
	RequiredStatusChecks *struct {
		Strict   bool     `json:"strict"`
		Contexts []string `json:"contexts"`
	} `json:"requiredStatusChecks,omitempty"`
	RequiredPullRequestReviews *struct {
		DismissStaleReviews          bool `json:"dismissStaleReviews"`
		RequireCodeOwnerReviews      bool `json:"requireCodeOwnerReviews"`
		RequiredApprovingReviewCount int  `json:"requiredApprovingReviewCount"`
	} `json:"requiredPullRequestReviews,omitempty"`
	RequiredSignatures             bool `json:"requiredSignatures"`
	EnforceAdmins                  bool `json:"enforceAdmins"`
	RequireLinearHistory           bool `json:"requireLinearHistory"`
	AllowForcePushes               bool `json:"allowForcePushes"`
	AllowDeletions                 bool `json:"allowDeletions"`
	RequiredConversationResolution bool `json:"requiredConversationResolution"`
	LockBranch                     bool `json:"lockBranch"`
	AllowForkSyncing               bool `json:"allowForkSyncing"`
}

// DeployKey represents a deploy key.
type DeployKey struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Key       string `json:"key"` // Public key (not the private key)
	ReadOnly  bool   `json:"readOnly"`
	Verified  bool   `json:"verified"`
	CreatedAt string `json:"createdAt"`
}

// Autolink represents an autolink reference.
type Autolink struct {
	ID          int64  `json:"id"`
	KeyPrefix   string `json:"keyPrefix"`
	URLTemplate string `json:"urlTemplate"`
}

// RepoActions holds Actions-related data for a repository.
type RepoActions struct {
	WorkflowsCount int              `json:"workflowsCount"`
	SecretsCount   int              `json:"secretsCount"`
	VariablesCount int              `json:"variablesCount"`
	RunnersCount   int              `json:"runnersCount"`
	CacheUsage     *CacheUsage      `json:"cacheUsage,omitempty"`
	Secrets        []SecretMetadata `json:"secrets,omitempty"`   // Kept for migration - metadata only
	Variables      []Variable       `json:"variables,omitempty"` // Kept for migration - metadata only
}

// CacheUsage represents Actions cache usage statistics.
type CacheUsage struct {
	ActiveCachesSizeInBytes int64 `json:"activeCachesSizeInBytes"`
	ActiveCachesCount       int   `json:"activeCachesCount"`
}

// Environment represents a deployment environment.
type Environment struct {
	ID              int64            `json:"id"`
	Name            string           `json:"name"`
	URL             string           `json:"url,omitempty"`
	ProtectionRules []ProtectionRule `json:"protectionRules,omitempty"`
	Secrets         []SecretMetadata `json:"secrets,omitempty"`
	Variables       []Variable       `json:"variables,omitempty"`
}

// ProtectionRule represents an environment protection rule.
type ProtectionRule struct {
	ID        int64      `json:"id"`
	Type      string     `json:"type"` // wait_timer, required_reviewers, branch_policy
	WaitTimer int        `json:"waitTimer,omitempty"`
	Reviewers []Reviewer `json:"reviewers,omitempty"`
}

// Reviewer represents a reviewer for environment protection.
type Reviewer struct {
	Type string `json:"type"` // User, Team
	ID   int64  `json:"id,omitempty"`
}

// RepoSecurity holds security-related data for a repository.
type RepoSecurity struct {
	Dependabot         *DependabotConfig     `json:"dependabot,omitempty"`
	CodeScanning       *CodeScanningConfig   `json:"codeScanning,omitempty"`
	SecretScanning     *SecretScanningConfig `json:"secretScanning,omitempty"`
	SecurityAdvisories []SecurityAdvisory    `json:"securityAdvisories,omitempty"`
}

// DependabotConfig holds Dependabot configuration and alert summary.
type DependabotConfig struct {
	Enabled     bool             `json:"enabled"`
	AlertCounts *AlertCounts     `json:"alertCounts,omitempty"`
	Secrets     []SecretMetadata `json:"secrets,omitempty"`
}

// CodeScanningConfig holds code scanning configuration and alert summary.
type CodeScanningConfig struct {
	DefaultSetup string       `json:"defaultSetup,omitempty"` // enabled, disabled, not-configured
	AlertCounts  *AlertCounts `json:"alertCounts,omitempty"`
}

// SecretScanningConfig holds secret scanning configuration and alerts.
type SecretScanningConfig struct {
	Enabled               bool         `json:"enabled"`
	PushProtectionEnabled bool         `json:"pushProtectionEnabled"`
	AlertCounts           *AlertCounts `json:"alertCounts,omitempty"`
}

// AlertCounts holds counts of alerts by severity/state.
type AlertCounts struct {
	Open   map[string]int `json:"open,omitempty"`   // By severity: critical, high, medium, low
	Closed map[string]int `json:"closed,omitempty"` // By severity
	Total  int            `json:"total"`
}

// SecurityAdvisory represents a repository security advisory.
type SecurityAdvisory struct {
	ID          string `json:"id"`
	Summary     string `json:"summary"`
	Description string `json:"description,omitempty"`
	Severity    string `json:"severity"`
	State       string `json:"state"`
	PublishedAt string `json:"publishedAt,omitempty"`
	UpdatedAt   string `json:"updatedAt"`
}

// License represents a repository license.
type License struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	SPDXID string `json:"spdxId"`
	URL    string `json:"url,omitempty"`
}

// PagesConfig represents GitHub Pages configuration.
type PagesConfig struct {
	URL        string `json:"url,omitempty"`
	Status     string `json:"status,omitempty"` // built, building, errored
	CNAME      string `json:"cname,omitempty"`
	Custom404  bool   `json:"custom404"`
	HTMLSource *struct {
		Branch string `json:"branch"`
		Path   string `json:"path"`
	} `json:"source,omitempty"`
	Public           bool `json:"public"`
	HTTPSEnforced    bool `json:"httpsEnforced,omitempty"`
	HTTPSCertificate *struct {
		State   string   `json:"state"`
		Domains []string `json:"domains"`
	} `json:"httpsCertificate,omitempty"`
}

// PackageData holds package information for a repository.
type PackageData struct {
	Org          string `json:"org"`
	Repo         string `json:"repo"`
	PackageName  string `json:"packageName"`
	PackageType  string `json:"packageType"`
	Visibility   string `json:"visibility"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
	VersionCount *int   `json:"versionCount,omitempty"` // Number of versions (nil if not available from API)
}

// PackageCounts holds both package count and version count for a repository.
type PackageCounts struct {
	PackageCount    int `json:"packageCount"`    // Total number of packages in the repository
	PackageVersions int `json:"packageVersions"` // Total sum of all package version counts
}

// fileExists checks if a file exists and is not a directory.
func fileExists(filePath string) bool {
	info, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

// IssuesData holds comprehensive issue statistics and metadata.
type IssuesData struct {
	OpenCount       int         `json:"openCount"`
	ClosedCount     int         `json:"closedCount"`
	TotalCount      int         `json:"totalCount"`
	LabelsCount     int         `json:"labelsCount"`
	MilestonesCount int         `json:"milestonesCount"`
	Milestones      []Milestone `json:"milestones,omitempty"` // Kept for migration - includes progress
}

// Milestone represents a repository milestone with progress.
type Milestone struct {
	Number       int    `json:"number"`
	Title        string `json:"title"`
	Description  string `json:"description,omitempty"`
	State        string `json:"state"` // open, closed
	OpenIssues   int    `json:"openIssues"`
	ClosedIssues int    `json:"closedIssues"`
	DueOn        string `json:"dueOn,omitempty"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
	ClosedAt     string `json:"closedAt,omitempty"`
}

// PullRequestsData holds comprehensive pull request statistics.
type PullRequestsData struct {
	OpenCount           int     `json:"openCount"`
	ClosedCount         int     `json:"closedCount"`
	MergedCount         int     `json:"mergedCount"`
	TotalCount          int     `json:"totalCount"`
	AvgTimeToMerge      string  `json:"avgTimeToMerge,omitempty"`      // Human-readable format (e.g., "2.5 days")
	AvgTimeToMergeHours float64 `json:"avgTimeToMergeHours,omitempty"` // For calculations
}

// TrafficData holds repository traffic statistics (last 14 days).
type TrafficData struct {
	Views  *TrafficStats `json:"views,omitempty"`
	Clones *TrafficStats `json:"clones,omitempty"`
}

// TrafficStats holds view or clone statistics.
type TrafficStats struct {
	Count   int             `json:"count"`
	Uniques int             `json:"uniques"`
	Details []TrafficDetail `json:"details,omitempty"`
}

// TrafficDetail holds daily traffic data.
type TrafficDetail struct {
	Timestamp string `json:"timestamp"`
	Count     int    `json:"count"`
	Uniques   int    `json:"uniques"`
}

// CommunityProfile holds repository community profile data.
type CommunityProfile struct {
	HealthPercentage int                    `json:"healthPercentage"`
	Description      string                 `json:"description,omitempty"`
	Documentation    string                 `json:"documentation,omitempty"`
	Files            *CommunityProfileFiles `json:"files,omitempty"`
	UpdatedAt        string                 `json:"updatedAt,omitempty"`
}

// CommunityProfileFiles holds status of community files.
type CommunityProfileFiles struct {
	CodeOfConduct       *CommunityFile `json:"codeOfConduct,omitempty"`
	CodeOfConductFile   *CommunityFile `json:"codeOfConductFile,omitempty"`
	Contributing        *CommunityFile `json:"contributing,omitempty"`
	IssueTemplate       *CommunityFile `json:"issueTemplate,omitempty"`
	PullRequestTemplate *CommunityFile `json:"pullRequestTemplate,omitempty"`
	License             *CommunityFile `json:"license,omitempty"`
	Readme              *CommunityFile `json:"readme,omitempty"`
}

// CommunityFile represents a community file's presence.
type CommunityFile struct {
	URL     string `json:"url,omitempty"`
	HTMLURL string `json:"htmlUrl,omitempty"`
}

// Project represents a repository project board (classic).
type Project struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Body         string `json:"body,omitempty"`
	Number       int    `json:"number"`
	State        string `json:"state"` // open, closed
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
	CreatorLogin string `json:"creatorLogin,omitempty"`
	ColumnsCount int    `json:"columnsCount,omitempty"`
}

// Release represents a repository release with assets.
type Release struct {
	ID              int64          `json:"id"`
	TagName         string         `json:"tagName"`
	Name            string         `json:"name,omitempty"`
	Body            string         `json:"body,omitempty"`
	Draft           bool           `json:"draft"`
	Prerelease      bool           `json:"prerelease"`
	CreatedAt       string         `json:"createdAt"`
	PublishedAt     string         `json:"publishedAt,omitempty"`
	AuthorLogin     string         `json:"authorLogin,omitempty"`
	TargetCommitish string         `json:"targetCommitish,omitempty"`
	Assets          []ReleaseAsset `json:"assets,omitempty"`
	DownloadCount   int            `json:"downloadCount,omitempty"` // Sum of all asset downloads
}

// ReleaseAsset represents a release asset file.
type ReleaseAsset struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Label         string `json:"label,omitempty"`
	ContentType   string `json:"contentType"`
	Size          int64  `json:"size"`
	DownloadCount int    `json:"downloadCount"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

// Deployment represents a repository deployment.
type Deployment struct {
	ID                    int64             `json:"id"`
	SHA                   string            `json:"sha"`
	Ref                   string            `json:"ref"`
	Task                  string            `json:"task"`
	Environment           string            `json:"environment,omitempty"`
	Description           string            `json:"description,omitempty"`
	CreatedAt             string            `json:"createdAt"`
	UpdatedAt             string            `json:"updatedAt"`
	CreatorLogin          string            `json:"creatorLogin,omitempty"`
	LatestStatus          *DeploymentStatus `json:"latestStatus,omitempty"`
	ProductionEnvironment bool              `json:"productionEnvironment"`
	TransientEnvironment  bool              `json:"transientEnvironment"`
}

// DeploymentStatus represents a deployment status.
type DeploymentStatus struct {
	ID           int64  `json:"id"`
	State        string `json:"state"` // error, failure, pending, in_progress, queued, success
	Description  string `json:"description,omitempty"`
	Environment  string `json:"environment,omitempty"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
	CreatorLogin string `json:"creatorLogin,omitempty"`
	LogURL       string `json:"logUrl,omitempty"`
}

// GitTag represents a detailed git tag.
type GitTag struct {
	Name       string     `json:"name"`
	SHA        string     `json:"sha"`
	ZipballURL string     `json:"zipballUrl,omitempty"`
	TarballURL string     `json:"tarballUrl,omitempty"`
	Commit     *TagCommit `json:"commit,omitempty"`
	NodeID     string     `json:"nodeId,omitempty"`
}

// TagCommit represents commit information for a tag.
type TagCommit struct {
	SHA string `json:"sha"`
	URL string `json:"url,omitempty"`
}

// GitReference represents a git reference (branch, tag, etc).

// RepoFiles holds important repository file content.
type RepoFiles struct {
	Readme     *RepoFileContent `json:"readme,omitempty"`
	CodeOwners *RepoFileContent `json:"codeowners,omitempty"`
}

// RepoFileContent represents file metadata and optionally content.
type RepoFileContent struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	SHA         string `json:"sha"`
	Size        int    `json:"size"`
	URL         string `json:"url,omitempty"`
	HTMLURL     string `json:"htmlUrl,omitempty"`
	DownloadURL string `json:"downloadUrl,omitempty"`
	Type        string `json:"type"`               // file, dir, symlink
	Content     string `json:"content,omitempty"`  // Base64 encoded if retrieved
	Encoding    string `json:"encoding,omitempty"` // base64, utf-8
}
