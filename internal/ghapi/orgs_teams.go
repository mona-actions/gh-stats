// Package ghapi provides GitHub API client functionality.
//
// This file (org_teams.go) contains organization team-related REST API functions.
// It provides functions to fetch team access data and repository permissions.
package ghapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mona-actions/gh-stats/internal/output"
	"github.com/mona-actions/gh-stats/internal/state"
	"github.com/pterm/pterm"
)

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

	// #nosec G204 - gh CLI command with controlled arguments (no user input in command construction)
	cmd := exec.CommandContext(ctx, "gh", args...)
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

		// #nosec G204 - gh CLI command with controlled arguments (no user input in command construction)
		cmd := exec.CommandContext(ctx, "gh", "api", "graphql",
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
