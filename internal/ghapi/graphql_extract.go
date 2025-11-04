// Package ghapi provides GitHub API client functionality.
//
// This file (graphql_extract.go) contains low-level data extraction functions.
// It provides functions to parse and extract data from GraphQL API responses.
package ghapi

import (
	"fmt"

	"github.com/mona-actions/gh-stats/internal/output"
)

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

// getKeys gets keys from a map for debugging.
func getKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// extractSettings extracts repository settings from GraphQL response.
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

// extractLanguages extracts language data from GraphQL response.
func extractLanguages(r map[string]interface{}) map[string]int {
	langsData, ok := r["languages"].(map[string]interface{})
	if !ok {
		return make(map[string]int)
	}

	edges, ok := langsData["edges"].([]interface{})
	if !ok {
		return make(map[string]int)
	}

	langs := make(map[string]int, len(edges))
	for _, edge := range edges {
		e, ok := edge.(map[string]interface{})
		if !ok {
			continue
		}

		size := 0
		if s, ok := e["size"].(float64); ok {
			size = int(s)
		}

		node, ok := e["node"].(map[string]interface{})
		if !ok {
			continue
		}

		name, ok := node["name"].(string)
		if ok {
			langs[name] = size
		}
	}

	return langs
}

// extractTopics extracts repository topics from GraphQL response.
func extractTopics(r map[string]interface{}) []string {
	topicsData, ok := r["repositoryTopics"].(map[string]interface{})
	if !ok {
		return []string{}
	}

	nodes, ok := topicsData["nodes"].([]interface{})
	if !ok {
		return []string{}
	}

	topics := make([]string, 0, len(nodes))
	for _, node := range nodes {
		n, ok := node.(map[string]interface{})
		if !ok {
			continue
		}

		topic, ok := n["topic"].(map[string]interface{})
		if !ok {
			continue
		}

		name, ok := topic["name"].(string)
		if ok {
			topics = append(topics, name)
		}
	}

	return topics
}

// extractLicense extracts license information from GraphQL response.
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

// extractDeployKeys extracts deploy keys from GraphQL response.
func extractDeployKeys(r map[string]interface{}) []output.DeployKey {
	deployKeysData, ok := r["deployKeys"].(map[string]interface{})
	if !ok {
		return []output.DeployKey{}
	}

	nodes, ok := deployKeysData["nodes"].([]interface{})
	if !ok {
		return []output.DeployKey{}
	}

	keys := make([]output.DeployKey, 0, len(nodes))
	for _, node := range nodes {
		n, ok := node.(map[string]interface{})
		if !ok {
			continue
		}

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

	return keys
}

// extractEnvironments extracts environment data from GraphQL response.
func extractEnvironments(r map[string]interface{}) []output.Environment {
	envsData, ok := r["environments"].(map[string]interface{})
	if !ok {
		return []output.Environment{}
	}

	nodes, ok := envsData["nodes"].([]interface{})
	if !ok {
		return []output.Environment{}
	}

	envs := make([]output.Environment, 0, len(nodes))
	for _, node := range nodes {
		n, ok := node.(map[string]interface{})
		if !ok {
			continue
		}

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

	return envs
}

// extractCollaborators extracts collaborator data with permissions from GraphQL response.
func extractCollaborators(r map[string]interface{}) []output.RepoCollaborator {
	collabsData, ok := r["collaborators"].(map[string]interface{})
	if !ok {
		return []output.RepoCollaborator{}
	}

	edges, ok := collabsData["edges"].([]interface{})
	if !ok {
		return []output.RepoCollaborator{}
	}

	collabs := make([]output.RepoCollaborator, 0, len(edges))
	for _, edge := range edges {
		e, ok := edge.(map[string]interface{})
		if !ok {
			continue
		}

		permission, _ := e["permission"].(string)
		node, ok := e["node"].(map[string]interface{})
		if !ok {
			continue
		}

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

	return collabs
}

// extractRulesets extracts repository rulesets from GraphQL response.
func extractRulesets(r map[string]interface{}) []output.Ruleset {
	rulesetsData, ok := r["rulesets"].(map[string]interface{})
	if !ok {
		return []output.Ruleset{}
	}

	nodes, ok := rulesetsData["nodes"].([]interface{})
	if !ok {
		return []output.Ruleset{}
	}

	rulesets := make([]output.Ruleset, 0, len(nodes))
	for _, node := range nodes {
		n, ok := node.(map[string]interface{})
		if !ok {
			continue
		}

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

	return rulesets
}

// extractMilestones extracts milestone data with issue counts from GraphQL response.
func extractMilestones(r map[string]interface{}) []output.Milestone {
	milestonesData, ok := r["milestones"].(map[string]interface{})
	if !ok {
		return []output.Milestone{}
	}

	nodes, ok := milestonesData["nodes"].([]interface{})
	if !ok {
		return []output.Milestone{}
	}

	milestones := make([]output.Milestone, 0, len(nodes))
	for _, node := range nodes {
		n, ok := node.(map[string]interface{})
		if !ok {
			continue
		}

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

	return milestones
}

// extractDeployments extracts deployment data from GraphQL response.
func extractDeployments(r map[string]interface{}) []output.Deployment {
	deploymentsData, ok := r["deployments"].(map[string]interface{})
	if !ok {
		return []output.Deployment{}
	}

	nodes, ok := deploymentsData["nodes"].([]interface{})
	if !ok {
		return []output.Deployment{}
	}

	deployments := make([]output.Deployment, 0, len(nodes))
	for _, node := range nodes {
		n, ok := node.(map[string]interface{})
		if !ok {
			continue
		}

		getString := func(key string) string {
			if val, ok := n[key].(string); ok {
				return val
			}
			return ""
		}

		var latestStatus *output.DeploymentStatus
		if ls, ok := n["latestStatus"].(map[string]interface{}); ok {
			state, _ := ls["state"].(string)
			desc, _ := ls["description"].(string)
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

	return deployments
}

// extractCommunityProfile extracts community profile data from GraphQL response.
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
