// Package ghapi provides functions for fetching comprehensive organization data from GitHub.
//
// Error Handling Strategy:
//   - CRITICAL errors (org details, auth, repo list): Return error immediately - operation cannot continue
//   - OPTIONAL features (webhooks, security managers, etc): Log warning and continue - nice-to-have data
//   - PARTIAL failures (some packages fail): Log warning, return partial results - don't fail entire operation
//
// This approach ensures maximum data collection even when some API endpoints fail due to
// permissions, rate limits, or service issues.
package ghapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mona-actions/gh-stats/internal/output"
	"github.com/mona-actions/gh-stats/internal/state"
	"github.com/pterm/pterm"
)

// fetchOrgSecurityAndSettings fetches security managers, custom properties, and roles.
func fetchOrgSecurityAndSettings(ctx context.Context, org string, verbose bool, orgMeta *output.OrgMetadata) {
	if verbose {
		pterm.Debug.Println("Fetching security managers...")
	}
	if securityManagers, err := fetchSecurityManagers(ctx, org, verbose); err != nil {
		pterm.Warning.Printf("Failed to fetch security managers: %v\n", err)
	} else {
		orgMeta.SecurityManagers = securityManagers
	}

	if verbose {
		pterm.Debug.Println("Fetching custom properties schema...")
	}
	if customProps, err := fetchCustomProperties(ctx, org, verbose); err != nil {
		pterm.Warning.Printf("Failed to fetch custom properties: %v\n", err)
	} else {
		orgMeta.CustomProperties = customProps
	}

	if verbose {
		pterm.Debug.Println("Fetching organization roles count...")
	}
	if orgRolesCount, err := fetchOrganizationRolesCount(ctx, org); err != nil {
		pterm.Warning.Printf("Failed to fetch organization roles count: %v\n", err)
	} else {
		orgMeta.OrganizationRolesCount = orgRolesCount
	}
}

// fetchOrgMembersAndTeams fetches member and team counts.
func fetchOrgMembersAndTeams(ctx context.Context, org string, verbose bool, orgMeta *output.OrgMetadata) {
	if verbose {
		pterm.Debug.Println("Fetching organization member count...")
	}
	if membersCount, err := fetchOrgMembersCount(ctx, org); err != nil {
		pterm.Warning.Printf("Failed to fetch members count: %v\n", err)
	} else {
		orgMeta.MembersCount = membersCount
	}

	if verbose {
		pterm.Debug.Println("Fetching outside collaborators count...")
	}
	if outsideCollabsCount, err := fetchOutsideCollaboratorsCount(ctx, org); err != nil {
		pterm.Warning.Printf("Failed to fetch outside collaborators count: %v\n", err)
	} else {
		orgMeta.OutsideCollaboratorsCount = outsideCollabsCount
	}

	if verbose {
		pterm.Debug.Println("Fetching teams count...")
	}
	if teamsCount, err := fetchTeamsCount(ctx, org); err != nil {
		pterm.Warning.Printf("Failed to fetch teams count: %v\n", err)
	} else {
		orgMeta.TeamsCount = teamsCount
	}
}

// fetchOrgAutomationAndCICD fetches webhooks, Actions secrets/variables, rulesets, and runners.
func fetchOrgAutomationAndCICD(ctx context.Context, org string, verbose bool, orgMeta *output.OrgMetadata) {
	if verbose {
		pterm.Debug.Println("Fetching webhooks...")
	}
	if webhooks, err := fetchOrgWebhooks(ctx, org, verbose); err != nil {
		pterm.Warning.Printf("Failed to fetch webhooks: %v\n", err)
	} else {
		orgMeta.Webhooks = webhooks
	}

	if verbose {
		pterm.Debug.Println("Fetching Actions secrets...")
	}
	if secrets, err := fetchActionsSecrets(ctx, org, verbose); err != nil {
		pterm.Warning.Printf("Failed to fetch Actions secrets: %v\n", err)
	} else {
		orgMeta.ActionsSecrets = secrets
	}

	if verbose {
		pterm.Debug.Println("Fetching Actions variables...")
	}
	if variables, err := fetchActionsVariables(ctx, org, verbose); err != nil {
		pterm.Warning.Printf("Failed to fetch Actions variables: %v\n", err)
	} else {
		orgMeta.ActionsVariables = variables
	}

	if verbose {
		pterm.Debug.Println("Fetching organization rulesets...")
	}
	if rulesets, err := fetchOrgRulesets(ctx, org, verbose); err != nil {
		pterm.Warning.Printf("Failed to fetch rulesets: %v\n", err)
	} else {
		orgMeta.Rulesets = rulesets
	}

	if verbose {
		pterm.Debug.Println("Fetching self-hosted runners count...")
	}
	if runnersCount, err := fetchRunnersCount(ctx, org); err != nil {
		pterm.Warning.Printf("Failed to fetch runners count: %v\n", err)
	} else {
		orgMeta.RunnersCount = runnersCount
	}
}

// fetchOrgAdditionalMetrics fetches blocked users, GitHub Apps, and packages.
func fetchOrgAdditionalMetrics(ctx context.Context, org string, verbose bool, skipPackages bool, orgMeta *output.OrgMetadata) {
	if verbose {
		pterm.Debug.Println("Fetching blocked users...")
	}
	if blockedUsers, err := fetchBlockedUsers(ctx, org); err != nil {
		pterm.Warning.Printf("Failed to fetch blocked users: %v\n", err)
	} else {
		orgMeta.BlockedUsers = blockedUsers
	}

	if verbose {
		pterm.Debug.Println("Fetching GitHub Apps installations...")
	}
	if appsCount, err := fetchGitHubAppsCount(ctx, org); err != nil {
		pterm.Warning.Printf("Failed to fetch GitHub Apps count: %v\n", err)
	} else {
		orgMeta.GitHubAppsCount = appsCount
	}

	// Skip org-level package count if requested
	if !skipPackages {
		if verbose {
			pterm.Debug.Println("Fetching org-level packages...")
		}
		packagesCount, err := fetchOrgPackagesCount(ctx, org)
		if err != nil {
			if verbose {
				pterm.Warning.Printf("Failed to fetch org packages count (continuing): %v\n", err)
			}
			packagesCount = 0
		}
		orgMeta.OrgPackagesCount = packagesCount
	} else if verbose {
		pterm.Info.Println("Skipping org-level package count (--no-packages flag set)")
	}
}

// GetEnhancedOrgMetadata fetches comprehensive organization metadata including all Phase 1 data.
func GetEnhancedOrgMetadata(ctx context.Context, org string, verbose bool, skipPackages bool) (output.OrgMetadata, error) {
	// Start with basic org info (CRITICAL - must succeed)
	orgMeta, err := fetchOrgDetails(ctx, org)
	if err != nil {
		return output.OrgMetadata{}, fmt.Errorf("fetching org details: %w", err)
	}

	if verbose {
		pterm.Info.Printf("Fetching comprehensive data for org: %s\n", org)
	}

	// Fetch different categories of org data
	fetchOrgSecurityAndSettings(ctx, org, verbose, &orgMeta)
	fetchOrgMembersAndTeams(ctx, org, verbose, &orgMeta)
	fetchOrgAutomationAndCICD(ctx, org, verbose, &orgMeta)
	fetchOrgAdditionalMetrics(ctx, org, verbose, skipPackages, &orgMeta)

	return orgMeta, nil
}

// FetchOrgTeamsAccess fetches all team access data for an organization using GraphQL with pagination.
// Returns a map of repo name -> team access data.
func FetchOrgTeamsAccess(ctx context.Context, org string, verbose bool) (map[string][]output.RepoTeamAccess, error) {
	repoTeamMap := make(map[string][]output.RepoTeamAccess)
	var teamsCursor *string
	teamsPage := 0

	for {
		teamsPage++
		if err := state.Get().CheckRateLimit(1); err != nil {
			return nil, fmt.Errorf("rate limit check failed on teams page %d: %w", teamsPage, err)
		}

		response, err := fetchTeamsPage(ctx, org, teamsCursor, teamsPage)
		if err != nil {
			return nil, err
		}

		if err := processTeamsPage(ctx, org, response.Data.Organization.Teams.Nodes, repoTeamMap, verbose); err != nil {
			return nil, err
		}

		if !response.Data.Organization.Teams.PageInfo.HasNextPage {
			break
		}

		teamsCursor = &response.Data.Organization.Teams.PageInfo.EndCursor

		if verbose && teamsPage%5 == 0 {
			pterm.Info.Printf("Fetched %d pages of teams for org %s...\n", teamsPage, org)
		}
	}

	if verbose && teamsPage > 1 {
		pterm.Success.Printf("Fetched all teams for org %s across %d pages\n", org, teamsPage)
	}

	return repoTeamMap, nil
}

// fetchTeamsPage fetches a single page of teams from GraphQL
func fetchTeamsPage(ctx context.Context, org string, teamsCursor *string, teamsPage int) (*teamsGraphQLResponse, error) {
	query := buildTeamsQuery(teamsCursor)
	args := buildTeamsQueryArgs(query, org, teamsCursor)

	cmd := BuildGHCommand(ctx, args...)
	cmd.Env = os.Environ()

	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to fetch team access (page %d): %w (stderr: %s)", teamsPage, err, errOut.String())
	}

	state.Get().IncrementAPICalls()

	var response teamsGraphQLResponse
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal team access response (page %d): %w", teamsPage, err)
	}

	return &response, nil
}

// teamsGraphQLResponse defines the structure for teams query response
type teamsGraphQLResponse struct {
	Data struct {
		Organization struct {
			Teams struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []struct {
					Name         string `json:"name"`
					Slug         string `json:"slug"`
					Repositories struct {
						PageInfo struct {
							HasNextPage bool   `json:"hasNextPage"`
							EndCursor   string `json:"endCursor"`
						} `json:"pageInfo"`
						Edges []struct {
							Permission string `json:"permission"`
							Node       struct {
								Name string `json:"name"`
							} `json:"node"`
						} `json:"edges"`
					} `json:"repositories"`
				} `json:"nodes"`
			} `json:"teams"`
		} `json:"organization"`
	} `json:"data"`
}

// buildTeamsQuery builds the GraphQL query with or without cursor
func buildTeamsQuery(teamsCursor *string) string {
	if teamsCursor == nil {
		return `query($org: String!) {
			organization(login: $org) {
				teams(first: 100) {
					pageInfo { hasNextPage endCursor }
					nodes {
						name
						slug
						repositories(first: 100) {
							pageInfo { hasNextPage endCursor }
							edges {
								permission
								node { name }
							}
						}
					}
				}
			}
		}`
	}
	return `query($org: String!, $teamsCursor: String!) {
		organization(login: $org) {
			teams(first: 100, after: $teamsCursor) {
				pageInfo { hasNextPage endCursor }
				nodes {
					name
					slug
					repositories(first: 100) {
						pageInfo { hasNextPage endCursor }
						edges {
							permission
							node { name }
						}
					}
				}
			}
		}
	}`
}

// buildTeamsQueryArgs builds the command arguments for the GraphQL query
func buildTeamsQueryArgs(query, org string, teamsCursor *string) []string {
	args := []string{"api", "graphql", "-f", fmt.Sprintf("query=%s", query), "-f", fmt.Sprintf("org=%s", org)}
	if teamsCursor != nil {
		args = append(args, "-f", fmt.Sprintf("teamsCursor=%s", *teamsCursor))
	}
	return args
}

// processTeamsPage processes all teams from a single page
func processTeamsPage(ctx context.Context, org string, teams []struct {
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	Repositories struct {
		PageInfo struct {
			HasNextPage bool   `json:"hasNextPage"`
			EndCursor   string `json:"endCursor"`
		} `json:"pageInfo"`
		Edges []struct {
			Permission string `json:"permission"`
			Node       struct {
				Name string `json:"name"`
			} `json:"node"`
		} `json:"edges"`
	} `json:"repositories"`
}, repoTeamMap map[string][]output.RepoTeamAccess, verbose bool) error {
	for _, team := range teams {
		processTeamRepositories(team, repoTeamMap)

		if team.Repositories.PageInfo.HasNextPage {
			if err := fetchTeamRepositories(ctx, org, team.Name, team.Slug, team.Repositories.PageInfo.EndCursor, repoTeamMap, verbose); err != nil {
				return fmt.Errorf("failed to fetch repositories for team %s: %w", team.Name, err)
			}
		}
	}
	return nil
}

// processTeamRepositories adds team access entries for all repositories in a team
func processTeamRepositories(team struct {
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	Repositories struct {
		PageInfo struct {
			HasNextPage bool   `json:"hasNextPage"`
			EndCursor   string `json:"endCursor"`
		} `json:"pageInfo"`
		Edges []struct {
			Permission string `json:"permission"`
			Node       struct {
				Name string `json:"name"`
			} `json:"node"`
		} `json:"edges"`
	} `json:"repositories"`
}, repoTeamMap map[string][]output.RepoTeamAccess) {
	for _, edge := range team.Repositories.Edges {
		repoName := edge.Node.Name
		access := output.RepoTeamAccess{
			TeamName:   team.Name,
			TeamSlug:   team.Slug,
			Permission: strings.ToUpper(edge.Permission), // GraphQL returns lowercase, REST returns uppercase
		}
		repoTeamMap[repoName] = append(repoTeamMap[repoName], access)
	}
}

// fetchTeamRepositories paginates through all repositories for a specific team.
// Updates the repoTeamMap with team access data for remaining repositories.
func fetchTeamRepositories(ctx context.Context, org, teamName, teamSlug, cursor string, repoTeamMap map[string][]output.RepoTeamAccess, verbose bool) error {
	reposPage := 1 // Starting from page 2 since first page was already processed
	currentCursor := cursor

	for currentCursor != "" {
		reposPage++
		if err := state.Get().CheckRateLimit(1); err != nil {
			return fmt.Errorf("rate limit check failed on team repos page %d: %w", reposPage, err)
		}

		query := `query($org: String!, $teamSlug: String!, $reposCursor: String!) {
			organization(login: $org) {
				team(slug: $teamSlug) {
					repositories(first: 100, after: $reposCursor) {
						pageInfo {
							hasNextPage
							endCursor
						}
						edges {
							permission
							node {
								name
							}
						}
					}
				}
			}
		}`

		cmd := BuildGHCommand(ctx, "api", "graphql",
			"-f", fmt.Sprintf("query=%s", query),
			"-f", fmt.Sprintf("org=%s", org),
			"-f", fmt.Sprintf("teamSlug=%s", teamSlug),
			"-f", fmt.Sprintf("reposCursor=%s", currentCursor))
		cmd.Env = os.Environ()

		var out bytes.Buffer
		var errOut bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errOut

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to fetch repositories page %d: %w (stderr: %s)", reposPage, err, errOut.String())
		}

		state.Get().IncrementAPICalls()

		var response struct {
			Data struct {
				Organization struct {
					Team struct {
						Repositories struct {
							PageInfo struct {
								HasNextPage bool   `json:"hasNextPage"`
								EndCursor   string `json:"endCursor"`
							} `json:"pageInfo"`
							Edges []struct {
								Permission string `json:"permission"`
								Node       struct {
									Name string `json:"name"`
								} `json:"node"`
							} `json:"edges"`
						} `json:"repositories"`
					} `json:"team"`
				} `json:"organization"`
			} `json:"data"`
		}

		if err := json.Unmarshal(out.Bytes(), &response); err != nil {
			return fmt.Errorf("failed to unmarshal repositories response (page %d): %w", reposPage, err)
		}

		// Process repositories from this page
		for _, edge := range response.Data.Organization.Team.Repositories.Edges {
			repoName := edge.Node.Name
			access := output.RepoTeamAccess{
				TeamName:   teamName,
				TeamSlug:   teamSlug,
				Permission: strings.ToUpper(edge.Permission),
			}
			repoTeamMap[repoName] = append(repoTeamMap[repoName], access)
		}

		// Check if we need to fetch more repositories
		if !response.Data.Organization.Team.Repositories.PageInfo.HasNextPage {
			break
		}

		currentCursor = response.Data.Organization.Team.Repositories.PageInfo.EndCursor
	}

	if verbose && reposPage > 1 {
		pterm.Info.Printf("Team '%s' has >100 repositories, fetched %d pages\n", teamName, reposPage)
	}

	return nil
}

// fetchOrgDetails fetches basic organization information.
func fetchOrgDetails(ctx context.Context, org string) (output.OrgMetadata, error) {
	if err := state.Get().CheckRateLimit(1); err != nil {
		return output.OrgMetadata{}, fmt.Errorf("rate limit check failed: %w", err)
	}

	cmd := BuildGHCommand(ctx, "api", fmt.Sprintf("/orgs/%s", org))
	outputBytes, err := cmd.Output()
	if err != nil {
		return output.OrgMetadata{}, fmt.Errorf("failed to fetch org details: %w", err)
	}

	var apiResp struct {
		Login                         string `json:"login"`
		ID                            int64  `json:"id"`
		NodeID                        string `json:"node_id"`
		URL                           string `json:"url"`
		Name                          string `json:"name"`
		Company                       string `json:"company"`
		Blog                          string `json:"blog"`
		Location                      string `json:"location"`
		Email                         string `json:"email"`
		TwitterUsername               string `json:"twitter_username"`
		Description                   string `json:"description"`
		PublicRepos                   int    `json:"public_repos"`
		PublicGists                   int    `json:"public_gists"`
		Followers                     int    `json:"followers"`
		Following                     int    `json:"following"`
		CreatedAt                     string `json:"created_at"`
		UpdatedAt                     string `json:"updated_at"`
		TotalPrivateRepos             int    `json:"total_private_repos"`
		OwnedPrivateRepos             int    `json:"owned_private_repos"`
		DefaultRepositoryPermission   string `json:"default_repository_permission"`
		TwoFactorRequirementEnabled   bool   `json:"two_factor_requirement_enabled"`
		MembersCanCreateRepositories  bool   `json:"members_can_create_repositories"`
		MembersCanCreatePublicRepos   bool   `json:"members_can_create_public_repositories"`
		MembersCanCreatePrivateRepos  bool   `json:"members_can_create_private_repositories"`
		MembersCanCreateInternalRepos bool   `json:"members_can_create_internal_repositories"`
		MembersCanForkPrivateRepos    bool   `json:"members_can_fork_private_repositories"`
		WebCommitSignoffRequired      bool   `json:"web_commit_signoff_required"`
	}

	if err := json.Unmarshal(outputBytes, &apiResp); err != nil {
		return output.OrgMetadata{}, fmt.Errorf("failed to unmarshal org details: %w", err)
	}

	orgMeta := output.OrgMetadata{
		Login:                         apiResp.Login,
		ID:                            apiResp.ID,
		NodeID:                        apiResp.NodeID,
		Name:                          apiResp.Name,
		Company:                       apiResp.Company,
		Blog:                          apiResp.Blog,
		Location:                      apiResp.Location,
		Email:                         apiResp.Email,
		Description:                   apiResp.Description,
		URL:                           apiResp.URL,
		TwitterUsername:               apiResp.TwitterUsername,
		PublicRepos:                   apiResp.PublicRepos,
		PublicGists:                   apiResp.PublicGists,
		Followers:                     apiResp.Followers,
		Following:                     apiResp.Following,
		TotalPrivateRepos:             apiResp.TotalPrivateRepos,
		OwnedPrivateRepos:             apiResp.OwnedPrivateRepos,
		DefaultRepoPermission:         apiResp.DefaultRepositoryPermission,
		TwoFactorRequirementEnabled:   apiResp.TwoFactorRequirementEnabled,
		MembersCanCreateRepos:         apiResp.MembersCanCreateRepositories,
		MembersCanCreatePublicRepos:   apiResp.MembersCanCreatePublicRepos,
		MembersCanCreatePrivateRepos:  apiResp.MembersCanCreatePrivateRepos,
		MembersCanCreateInternalRepos: apiResp.MembersCanCreateInternalRepos,
		MembersCanForkPrivateRepos:    apiResp.MembersCanForkPrivateRepos,
		WebCommitSignoffRequired:      apiResp.WebCommitSignoffRequired,
		CreatedAt:                     apiResp.CreatedAt,
		UpdatedAt:                     apiResp.UpdatedAt,
	}

	return orgMeta, nil
}

// fetchSecurityManagers fetches teams designated as security managers.
func fetchSecurityManagers(ctx context.Context, org string, verbose bool) ([]output.SecurityManager, error) {
	if err := state.Get().CheckRateLimit(1); err != nil {
		return nil, fmt.Errorf("rate limit check failed: %w", err)
	}

	cmd := BuildGHCommand(ctx, "api", fmt.Sprintf("/orgs/%s/security-managers", org), "--paginate")
	outputBytes, err := cmd.Output()
	if err != nil {
		// This might fail if the feature isn't available, that's okay
		return nil, nil
	}

	var apiResp []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	}

	if err := json.Unmarshal(outputBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal security managers: %w", err)
	}

	managers := make([]output.SecurityManager, len(apiResp))
	for i, team := range apiResp {
		managers[i] = output.SecurityManager{
			TeamID:   team.ID,
			TeamName: team.Name,
			TeamSlug: team.Slug,
		}
	}

	if verbose {
		pterm.Debug.Printf("Found %d security manager team(s)\n", len(managers))
	}

	return managers, nil
}

// fetchCustomProperties fetches custom property definitions.
func fetchCustomProperties(ctx context.Context, org string, verbose bool) ([]output.CustomProperty, error) {
	if err := state.Get().CheckRateLimit(1); err != nil {
		return nil, fmt.Errorf("rate limit check failed: %w", err)
	}

	cmd := BuildGHCommand(ctx, "api", fmt.Sprintf("/orgs/%s/properties/schema", org))
	outputBytes, err := cmd.Output()
	if err != nil {
		// Feature may not be available
		return nil, nil
	}

	var apiResp []struct {
		PropertyName  string   `json:"property_name"`
		ValueType     string   `json:"value_type"`
		Required      bool     `json:"required"`
		DefaultValue  string   `json:"default_value"`
		Description   string   `json:"description"`
		AllowedValues []string `json:"allowed_values"`
	}

	if err := json.Unmarshal(outputBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal custom properties: %w", err)
	}

	props := make([]output.CustomProperty, len(apiResp))
	for i, prop := range apiResp {
		props[i] = output.CustomProperty{
			PropertyName:  prop.PropertyName,
			ValueType:     prop.ValueType,
			Required:      prop.Required,
			DefaultValue:  prop.DefaultValue,
			Description:   prop.Description,
			AllowedValues: prop.AllowedValues,
		}
	}

	if verbose {
		pterm.Debug.Printf("Found %d custom propert(ies)\n", len(props))
	}

	return props, nil
}

// fetchOrganizationRolesCount fetches the count of custom organization roles.
func fetchOrganizationRolesCount(ctx context.Context, org string) (int, error) {
	if err := state.Get().CheckRateLimit(1); err != nil {
		return 0, fmt.Errorf("rate limit check failed: %w", err)
	}

	cmd := BuildGHCommand(ctx, "api", fmt.Sprintf("/orgs/%s/organization-roles", org))
	outputBytes, err := cmd.Output()
	if err != nil {
		// Feature may not be available
		return 0, nil
	}

	var apiResp struct {
		Roles []struct {
			ID int64 `json:"id"`
		} `json:"roles"`
	}

	if err := json.Unmarshal(outputBytes, &apiResp); err != nil {
		return 0, fmt.Errorf("failed to unmarshal organization roles: %w", err)
	}

	return len(apiResp.Roles), nil
}

// fetchOrgMembersCount fetches the count of organization members.
func fetchOrgMembersCount(ctx context.Context, org string) (int, error) {
	return getCountFromLinkHeader(ctx, fmt.Sprintf("/orgs/%s/members", org))
}

// fetchOutsideCollaboratorsCount fetches the count of outside collaborators.
func fetchOutsideCollaboratorsCount(ctx context.Context, org string) (int, error) {
	return getCountFromLinkHeader(ctx, fmt.Sprintf("/orgs/%s/outside_collaborators", org))
}

// fetchTeamsCount fetches the count of teams in the organization.
func fetchTeamsCount(ctx context.Context, org string) (int, error) {
	if err := state.Get().CheckRateLimit(1); err != nil {
		return 0, fmt.Errorf("rate limit check failed: %w", err)
	}

	cmd := BuildGHCommand(ctx, "api", fmt.Sprintf("/orgs/%s/teams?per_page=1", org), "--paginate")
	outputBytes, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("failed to fetch teams count: %w", err)
	}

	var apiResp []struct {
		ID int64 `json:"id"`
	}

	if err := json.Unmarshal(outputBytes, &apiResp); err != nil {
		return 0, fmt.Errorf("failed to unmarshal teams: %w", err)
	}

	return len(apiResp), nil
}

// fetchOrgWebhooks fetches organization-level webhooks.
func fetchOrgWebhooks(ctx context.Context, org string, verbose bool) ([]output.Webhook, error) {
	if err := state.Get().CheckRateLimit(1); err != nil {
		return nil, fmt.Errorf("rate limit check failed: %w", err)
	}

	cmd := BuildGHCommand(ctx, "api", fmt.Sprintf("/orgs/%s/hooks", org), "--paginate")
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to fetch webhooks: %w (stderr: %s)", err, stderr.String())
	}

	outputBytes := stdout.Bytes()

	var apiResp []struct {
		ID     int64    `json:"id"`
		Name   string   `json:"name"`
		Active bool     `json:"active"`
		Events []string `json:"events"`
		Config struct {
			URL         string `json:"url"`
			ContentType string `json:"content_type"`
			InsecureSSL string `json:"insecure_ssl"`
		} `json:"config"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
	}

	if err := json.Unmarshal(outputBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal webhooks: %w", err)
	}

	webhooks := make([]output.Webhook, len(apiResp))
	for i, hook := range apiResp {
		webhooks[i] = output.Webhook{
			ID:     hook.ID,
			Name:   hook.Name,
			Active: hook.Active,
			Events: hook.Events,
			Config: struct {
				URL         string `json:"url"`
				ContentType string `json:"contentType"`
				InsecureSSL string `json:"insecureSsl"`
			}{
				URL:         hook.Config.URL,
				ContentType: hook.Config.ContentType,
				InsecureSSL: hook.Config.InsecureSSL,
			},
			CreatedAt: hook.CreatedAt,
			UpdatedAt: hook.UpdatedAt,
		}
	}

	if verbose {
		pterm.Debug.Printf("Found %d webhook(s)\n", len(webhooks))
	}

	return webhooks, nil
}

// fetchActionsSecrets fetches Actions secrets (metadata only, not values).
func fetchActionsSecrets(ctx context.Context, org string, verbose bool) ([]output.SecretMetadata, error) {
	if err := state.Get().CheckRateLimit(1); err != nil {
		return nil, fmt.Errorf("rate limit check failed: %w", err)
	}

	cmd := BuildGHCommand(ctx, "api", fmt.Sprintf("/orgs/%s/actions/secrets", org), "--paginate")
	outputBytes, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Actions secrets: %w", err)
	}

	var apiResp struct {
		Secrets []struct {
			Name       string `json:"name"`
			CreatedAt  string `json:"created_at"`
			UpdatedAt  string `json:"updated_at"`
			Visibility string `json:"visibility"`
		} `json:"secrets"`
	}

	if err := json.Unmarshal(outputBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Actions secrets: %w", err)
	}

	secrets := make([]output.SecretMetadata, len(apiResp.Secrets))
	for i, secret := range apiResp.Secrets {
		secrets[i] = output.SecretMetadata{
			Name:       secret.Name,
			CreatedAt:  secret.CreatedAt,
			UpdatedAt:  secret.UpdatedAt,
			Visibility: secret.Visibility,
		}
	}

	if verbose {
		pterm.Debug.Printf("Found %d Actions secret(s)\n", len(secrets))
	}

	return secrets, nil
}

// fetchActionsVariables fetches Actions variables.
func fetchActionsVariables(ctx context.Context, org string, verbose bool) ([]output.Variable, error) {
	if err := state.Get().CheckRateLimit(1); err != nil {
		return nil, fmt.Errorf("rate limit check failed: %w", err)
	}

	cmd := BuildGHCommand(ctx, "api", fmt.Sprintf("/orgs/%s/actions/variables", org), "--paginate")
	outputBytes, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Actions variables: %w", err)
	}

	var apiResp struct {
		Variables []struct {
			Name       string `json:"name"`
			Value      string `json:"value"`
			CreatedAt  string `json:"created_at"`
			UpdatedAt  string `json:"updated_at"`
			Visibility string `json:"visibility"`
		} `json:"variables"`
	}

	if err := json.Unmarshal(outputBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Actions variables: %w", err)
	}

	variables := make([]output.Variable, len(apiResp.Variables))
	for i, variable := range apiResp.Variables {
		variables[i] = output.Variable{
			Name:       variable.Name,
			Value:      variable.Value,
			CreatedAt:  variable.CreatedAt,
			UpdatedAt:  variable.UpdatedAt,
			Visibility: variable.Visibility,
		}
	}

	if verbose {
		pterm.Debug.Printf("Found %d Actions variable(s)\n", len(variables))
	}

	return variables, nil
}

// fetchOrgRulesets fetches organization-level rulesets.
func fetchOrgRulesets(ctx context.Context, org string, verbose bool) ([]output.Ruleset, error) {
	if err := state.Get().CheckRateLimit(1); err != nil {
		return nil, fmt.Errorf("rate limit check failed: %w", err)
	}

	cmd := BuildGHCommand(ctx, "api", fmt.Sprintf("/orgs/%s/rulesets", org), "--paginate")
	outputBytes, err := cmd.Output()
	if err != nil {
		// Rulesets may not be available
		return nil, nil
	}

	var apiResp []struct {
		ID           int64  `json:"id"`
		Name         string `json:"name"`
		Target       string `json:"target"`
		SourceType   string `json:"source_type"`
		Source       string `json:"source"`
		Enforcement  string `json:"enforcement"`
		BypassActors []struct {
			ActorID    int64  `json:"actor_id"`
			ActorType  string `json:"actor_type"`
			BypassMode string `json:"bypass_mode"`
		} `json:"bypass_actors"`
		Conditions interface{} `json:"conditions"`
		Rules      []struct {
			Type       string      `json:"type"`
			Parameters interface{} `json:"parameters"`
		} `json:"rules"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
	}

	if err := json.Unmarshal(outputBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal rulesets: %w", err)
	}

	rulesets := make([]output.Ruleset, len(apiResp))
	for i, ruleset := range apiResp {
		bypassActors := make([]output.BypassActor, len(ruleset.BypassActors))
		for j, actor := range ruleset.BypassActors {
			bypassActors[j] = output.BypassActor{
				ActorID:    actor.ActorID,
				ActorType:  actor.ActorType,
				BypassMode: actor.BypassMode,
			}
		}

		rules := make([]output.Rule, len(ruleset.Rules))
		for j, rule := range ruleset.Rules {
			rules[j] = output.Rule{
				Type:       rule.Type,
				Parameters: rule.Parameters,
			}
		}

		rulesets[i] = output.Ruleset{
			ID:           ruleset.ID,
			Name:         ruleset.Name,
			Target:       ruleset.Target,
			SourceType:   ruleset.SourceType,
			Source:       ruleset.Source,
			Enforcement:  ruleset.Enforcement,
			BypassActors: bypassActors,
			Conditions:   ruleset.Conditions,
			Rules:        rules,
			CreatedAt:    ruleset.CreatedAt,
			UpdatedAt:    ruleset.UpdatedAt,
		}
	}

	if verbose {
		pterm.Debug.Printf("Found %d ruleset(s)\n", len(rulesets))
	}

	return rulesets, nil
}

// fetchRunnersCount fetches the total count of self-hosted runners across all groups.
func fetchRunnersCount(ctx context.Context, org string) (int, error) {
	if err := state.Get().CheckRateLimit(1); err != nil {
		return 0, fmt.Errorf("rate limit check failed: %w", err)
	}

	cmd := BuildGHCommand(ctx, "api", fmt.Sprintf("/orgs/%s/actions/runners?per_page=1", org), "--paginate")
	outputBytes, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("failed to fetch runners count: %w", err)
	}

	var apiResp struct {
		Runners []struct {
			ID int64 `json:"id"`
		} `json:"runners"`
	}

	if err := json.Unmarshal(outputBytes, &apiResp); err != nil {
		return 0, fmt.Errorf("failed to unmarshal runners: %w", err)
	}

	return len(apiResp.Runners), nil
}

// fetchBlockedUsers fetches users blocked by the organization.
func fetchBlockedUsers(ctx context.Context, org string) ([]output.BlockedUser, error) {
	if err := state.Get().CheckRateLimit(1); err != nil {
		return nil, fmt.Errorf("rate limit check failed: %w", err)
	}

	cmd := BuildGHCommand(ctx, "api", "--paginate", fmt.Sprintf("/orgs/%s/blocks", org))
	cmd.Env = os.Environ()

	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to fetch blocked users: %w (stderr: %s)", err, errOut.String())
	}

	state.Get().IncrementAPICalls()

	var apiResp []struct {
		Login     string `json:"login"`
		ID        int64  `json:"id"`
		NodeID    string `json:"node_id"`
		AvatarURL string `json:"avatar_url"`
		Type      string `json:"type"`
		SiteAdmin bool   `json:"site_admin"`
	}

	if err := json.Unmarshal(out.Bytes(), &apiResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal blocked users: %w", err)
	}

	blockedUsers := make([]output.BlockedUser, len(apiResp))
	for i, user := range apiResp {
		blockedUsers[i] = output.BlockedUser{
			Login:     user.Login,
			ID:        user.ID,
			NodeID:    user.NodeID,
			AvatarURL: user.AvatarURL,
			Type:      user.Type,
			SiteAdmin: user.SiteAdmin,
		}
	}

	return blockedUsers, nil
}

// fetchGitHubAppsCount fetches the count of GitHub App installations for an organization.
func fetchGitHubAppsCount(ctx context.Context, org string) (int, error) {
	if err := state.Get().CheckRateLimit(1); err != nil {
		return 0, fmt.Errorf("rate limit check failed: %w", err)
	}

	cmd := BuildGHCommand(ctx, "api", fmt.Sprintf("/orgs/%s/installations", org), "--paginate")
	cmd.Env = os.Environ()

	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("failed to fetch GitHub Apps: %w (stderr: %s)", err, errOut.String())
	}

	state.Get().IncrementAPICalls()

	var apiResp struct {
		TotalCount    int `json:"total_count"`
		Installations []struct {
			ID int64 `json:"id"`
		} `json:"installations"`
	}

	if err := json.Unmarshal(out.Bytes(), &apiResp); err != nil {
		return 0, fmt.Errorf("failed to unmarshal GitHub Apps: %w", err)
	}

	// Return either total_count if available, or length of installations array
	if apiResp.TotalCount > 0 {
		return apiResp.TotalCount, nil
	}
	return len(apiResp.Installations), nil
}

// fetchOrgPackagesCount fetches the count of packages at the organization level.
func fetchOrgPackagesCount(ctx context.Context, org string) (int, error) {
	// GitHub Packages API requires package_type parameter
	// Valid types: npm, maven, rubygems, docker, nuget, container
	packageTypes := []string{"npm", "maven", "rubygems", "docker", "nuget", "container"}

	totalCount := 0

	for _, pkgType := range packageTypes {
		count, err := fetchOrgPackagesByType(ctx, org, pkgType)
		if err != nil {
			// If we get 404 or 403, the org might not have access to this package type
			if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "403") {
				continue // Skip this package type
			}
			return totalCount, err
		}
		totalCount += count
	}

	return totalCount, nil
}

// fetchOrgPackagesByType fetches packages of a specific type for an organization.
// retryPackageFetch attempts to fetch packages with retries for gateway errors
// handlePackageFetchError handles errors and determines retry behavior
func handlePackageFetchError(ctx context.Context, errStr, packageType string, pageNum, attempt, maxRetries int) (shouldContinue bool, err error) {
	// Check for gateway errors
	if strings.Contains(errStr, "504") || strings.Contains(errStr, "502") ||
		strings.Contains(errStr, "503") || strings.Contains(errStr, "500") {
		if attempt < maxRetries-1 {
			backoff := time.Duration(1<<uint(attempt)) * time.Second
			if backoff > 10*time.Second {
				backoff = 10 * time.Second
			}
			pterm.Warning.Printf("⚠ Gateway error fetching %s packages (page %d), retrying in %v (attempt %d/%d)\n",
				packageType, pageNum, backoff, attempt+1, maxRetries)

			select {
			case <-ctx.Done():
				pterm.Info.Println("Operation cancelled, stopping retries")
				return false, ctx.Err()
			case <-time.After(backoff):
				return true, nil // Continue retrying
			}
		}
		// After all retries, return partial success
		pterm.Warning.Printf("⚠ Gateway error persists for %s packages after %d retries, continuing with partial results\n",
			packageType, maxRetries)
		return false, nil // Stop retrying, partial success
	}

	// For other errors, retry with backoff
	if attempt < maxRetries-1 {
		backoff := time.Duration(1<<uint(attempt)) * time.Second
		select {
		case <-ctx.Done():
			pterm.Info.Println("Operation cancelled, stopping retries")
			return false, ctx.Err()
		case <-time.After(backoff):
			return true, nil // Continue retrying
		}
	}
	return false, fmt.Errorf("failed to fetch %s packages: %s", packageType, errStr)
}

func retryPackageFetch(ctx context.Context, org, packageType string, pageNum, maxRetries int) ([]struct {
	ID int64 `json:"id"`
}, error) {
	var packages []struct {
		ID int64 `json:"id"`
	}

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Check for context cancellation before retry
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return packages, ctx.Err()
			default:
			}
		}

		cmd := BuildGHCommand(ctx, "api", fmt.Sprintf("/orgs/%s/packages?package_type=%s&per_page=100&page=%d", org, packageType, pageNum))
		cmd.Env = os.Environ()

		var out bytes.Buffer
		var errOut bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errOut

		if err := cmd.Run(); err != nil {
			shouldContinue, handledErr := handlePackageFetchError(ctx, errOut.String(), packageType, pageNum, attempt, maxRetries)
			if !shouldContinue {
				return packages, handledErr
			}
			continue
		}

		state.Get().IncrementAPICalls()

		if err := json.Unmarshal(out.Bytes(), &packages); err != nil {
			return packages, fmt.Errorf("failed to unmarshal %s packages: %w", packageType, err)
		}

		// Success!
		return packages, nil
	}

	return packages, fmt.Errorf("max retries reached for %s packages", packageType)
}

func fetchOrgPackagesByType(ctx context.Context, org, packageType string) (int, error) {
	if err := state.Get().CheckRateLimit(1); err != nil {
		return 0, fmt.Errorf("rate limit check failed: %w", err)
	}

	var count int
	pageNum := 1
	maxPages := 10
	maxRetries := 3

	for pageNum <= maxPages {
		packages, err := retryPackageFetch(ctx, org, packageType, pageNum, maxRetries)
		if err != nil {
			// Context cancellation or non-recoverable error
			if ctx.Err() != nil {
				return count, ctx.Err()
			}
			// Other errors return current count
			return count, err
		}

		if len(packages) == 0 {
			break
		}

		count += len(packages)

		if len(packages) < 100 {
			break
		}

		pageNum++
	}

	return count, nil
}
