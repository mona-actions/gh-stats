# Developer Guide

This guide helps contributors understand the `gh-stats` architecture and provides step-by-step instructions for common development tasks.

## Table of Contents

- [Architecture Overview](#architecture-overview)
- [Adding a New API Call](#adding-a-new-api-call)
  - [REST API Example](#rest-api-example)
  - [GraphQL API Example](#graphql-api-example)
- [Adding a New Feature Flag](#adding-a-new-feature-flag)
- [Adding a New Output Field](#adding-a-new-output-field)
- [Testing Changes](#testing-changes)
- [Best Practices](#best-practices)
- [Common Patterns](#common-patterns)

## Architecture Overview

`gh-stats` uses a layered architecture with clear separation of concerns:

```
┌─────────────────────────────────────────────────────┐
│ CLI Layer (cmd/)                                     │
│ - Command parsing and validation                    │
└─────────────────┬───────────────────────────────────┘
                  │
┌─────────────────▼───────────────────────────────────┐
│ Orchestration Layer (internal/stats/)               │
│ - Main coordinator (processor.go)                   │
│ - Worker pool management (concurrency.go)           │
│ - Input validation (validation.go)                  │
└─────────────────┬───────────────────────────────────┘
                  │
┌─────────────────▼───────────────────────────────────┐
│ API Layer (internal/ghapi/)                         │
│ - GraphQL queries (graphql_*.go)                    │
│ - REST API calls (repos_*.go, orgs_*.go)            │
│ - Rate limiting (client_ratelimit.go)               │
│ - Retry logic (client_retry.go)                     │
│ - REST operations (client_rest.go)                  │
│ - Pagination (client_pagination.go)                 │
│ - Types (client_types.go, repos_types.go)           │
└─────────────────┬───────────────────────────────────┘
                  │
┌─────────────────▼───────────────────────────────────┐
│ State Management (internal/state/)                  │
│ - Global state & synchronization (state.go)         │
│ - Rate limit tracking                               │
│ - Progress tracking                                 │
└─────────────────────────────────────────────────────┘
                  │
┌─────────────────▼───────────────────────────────────┐
│ Output Layer (internal/output/)                     │
│ - Data models (models.go)                           │
│ - JSON serialization (json.go)                      │
└─────────────────────────────────────────────────────┘
```

### Key Design Principles

1. **Rate Limit Awareness**: All API calls go through the rate limiter
2. **Incremental Output**: Data is written immediately to prevent loss on interruption
3. **Error Resilience**: Non-critical errors are logged as warnings; processing continues
4. **Concurrent Processing**: Worker pool pattern with configurable concurrency
5. **Context Propagation**: Graceful cancellation support (Ctrl-C)

## Adding a New API Call

This section shows complete end-to-end examples for adding new data collection features.

### REST API Example: Adding Webhook Count

**Scenario**: Add a feature to count webhooks per repository.

**Complete Flow**:

#### 1. Add the feature flag

**File**: `cmd/root.go`

```go
// Add flag variable (around line 22)
var (
    // ... existing flags ...
    noWebhooks       bool
)

// Add flag definition in init() (around line 228)
rootCmd.Flags().BoolVar(&noWebhooks, "no-webhooks", false, "Skip fetching webhook information")
```

#### 2. Add to DataFetchConfig

**File**: `internal/ghapi/repos_types.go`

```go
type DataFetchConfig struct {
    // ... existing fields ...
    FetchWebhooks        bool
}
```

#### 3. Wire up the flag to config

**File**: `cmd/root.go` in `rootCmd.RunE` (around line 100)

```go
// Apply minimal mode overrides
if minimal {
    // ... existing no-* flags ...
    noWebhooks = true
}

// Apply CLI overrides
if cmd.Flags().Changed("no-webhooks") {
    noWebhooks, _ = cmd.Flags().GetBool("no-webhooks")
}

// Build config
config := stats.Config{
    // ... existing fields ...
    DataFetchConfig: ghapi.DataFetchConfig{
        // ... existing fields ...
        FetchWebhooks: !noWebhooks,
    },
}
```

#### 4. Add the data model field

**File**: `internal/output/models.go`

```go
type RepoStats struct {
    // ... existing fields ...
    
    // In the Automation section:
    Webhooks         int                `json:"webhooks"`
}
```

#### 5. Create the REST API function

**File**: `internal/ghapi/repos_webhooks.go` (new file)

```go
package ghapi

import (
    "context"
    "encoding/json"
    "fmt"
    
    "github.com/mona-actions/gh-stats/internal/output"
)

// fetchRepoWebhooks fetches webhook count for a repository.
func fetchRepoWebhooks(ctx context.Context, org, repo string, stats *output.RepoStats) error {
    var webhooks []struct {
        ID     int64  `json:"id"`
        Name   string `json:"name"`
        Active bool   `json:"active"`
    }
    
    endpoint := fmt.Sprintf("/repos/%s/%s/hooks", org, repo)
    
    err := RunRESTCallback(ctx, endpoint, func(data []byte) error {
        var page []struct {
            ID     int64  `json:"id"`
            Name   string `json:"name"`
            Active bool   `json:"active"`
        }
        
        if err := json.Unmarshal(data, &page); err != nil {
            return err
        }
        
        webhooks = append(webhooks, page...)
        return nil
    })
    
    if err != nil {
        return err
    }
    
    stats.Webhooks = len(webhooks)
    return nil
}
```

#### 6. Integrate into data collection

**File**: `internal/ghapi/repos_metadata.go`

Option A - Add to existing fetch function:

```go
// Find fetchRepoAutomationData (around line 105) and add:
func fetchRepoAutomationData(ctx context.Context, org, repo string, fetchConfig DataFetchConfig, stats *output.RepoStats) {
    if fetchConfig.FetchWebhooks {
        if err := fetchRepoWebhooks(ctx, org, repo, stats); err != nil {
            pterm.Warning.Printf("Failed to fetch webhooks for %s/%s: %v\n", org, repo, err)
        }
    }
    
    // ... rest of function ...
}
```

Option B - Create new category function:

```go
// Add to GetEnhancedRepoData (around line 200):
func GetEnhancedRepoData(ctx context.Context, org, repo string, teamAccessMap map[string][]output.RepoTeamAccess, fetchConfig DataFetchConfig) (*output.RepoStats, error) {
    stats := &output.RepoStats{
        Org:  org,
        Name: repo,
    }

    // Fetch different categories of data
    fetchRepoSettingsAndAccess(ctx, org, repo, teamAccessMap, fetchConfig, stats)
    fetchRepoAutomationData(ctx, org, repo, fetchConfig, stats)
    fetchRepoWebhookData(ctx, org, repo, fetchConfig, stats)  // NEW
    fetchRepoIssuesAndPRs(ctx, org, repo, fetchConfig, stats)
    // ... rest ...

    return stats, nil
}

// Add new category function:
func fetchRepoWebhookData(ctx context.Context, org, repo string, fetchConfig DataFetchConfig, stats *output.RepoStats) {
    if fetchConfig.FetchWebhooks {
        if err := fetchWebhooks(ctx, org, repo, stats); err != nil {
            fmt.Printf(" WARNING Could not fetch webhooks for %s/%s: %v\n", org, repo, err)
        }
    }
}
```

#### 7. Update documentation

**File**: `README.md` - Add to feature flags table:

```markdown
| `--no-webhooks` | Webhook configurations | ~1 call/repo | Webhook counts and active status |
```

#### 8. Test the feature

```bash
# Build
go build -o gh-stats

# Test with flag enabled (default)
./gh-stats --org test-org --verbose

# Test with flag disabled
./gh-stats --org test-org --no-webhooks --verbose

# Verify JSON output
cat gh-stats-*.json | jq '.repos[0].webhooks'
```

**Complete!** You've now added full webhook collection with feature flag control.

### GraphQL API Example: Adding Deployment Count

**Scenario**: Add a feature to count deployments per repository using GraphQL.

**Complete Flow**:

#### 1. Add the feature flag

**File**: `cmd/root.go`

```go
// Add flag variable (around line 22)
var (
    // ... existing flags ...
    noDeployments    bool
)

// Add flag definition in init() (around line 228)
rootCmd.Flags().BoolVar(&noDeployments, "no-deployments", false, "Skip fetching deployment information")
```

#### 2. Add to DataFetchConfig

**File**: `internal/ghapi/repos_types.go`

```go
type DataFetchConfig struct {
    // ... existing fields ...
    FetchDeployments     bool
}
```

#### 3. Wire up the flag to config

**File**: `cmd/root.go` in `rootCmd.RunE`

```go
// Apply minimal mode overrides
if minimal {
    // ... existing no-* flags ...
    noDeployments = true
}

// Apply CLI overrides
if cmd.Flags().Changed("no-deployments") {
    noDeployments, _ = cmd.Flags().GetBool("no-deployments")
}

// Build config
config := stats.Config{
    // ... existing fields ...
    DataFetchConfig: ghapi.DataFetchConfig{
        // ... existing fields ...
        FetchDeployments: !noDeployments,
    },
}
```

#### 4. Add the data model field

**File**: `internal/output/models.go`

```go
type RepoStats struct {
    // ... existing fields ...
    
    // Add in appropriate section (e.g., CI/CD):
    Deployments      int                `json:"deployments"`
}
```

#### 5. Update the GraphQL query

**File**: `internal/ghapi/graphql_queries.go`

Find the `getRepositoryFields` function (around line 570) and add the new field:

```go
func getRepositoryFields(config DataFetchConfig) string {
    fields := `				name
                nameWithOwner
                # ... existing base fields ...
`
    
    // Add deployments field conditionally
    if config.FetchDeployments {
        fields += `				deployments(first: 0) {
                    totalCount
                }
`
    }
    
    // ... rest of fields ...
    
    return fields
}
```

**Important**: If this is an "expensive" field (slow query), add it to the expensive fields section instead:

```go
// In getExpensiveOnlyConfig function (around line 60):
func getExpensiveOnlyConfig(base DataFetchConfig) DataFetchConfig {
    return DataFetchConfig{
        // ... existing expensive fields ...
        FetchDeployments:  base.FetchDeployments,
    }
}

// Then add to fetchExpensiveGraphQLFields query (around line 320)
```

#### 6. Create the extraction function

**File**: `internal/ghapi/graphql_extract.go`

```go
// Add extraction helper function (around line 280):
func extractDeployments(r map[string]interface{}) int {
    deploymentsData, ok := r["deployments"].(map[string]interface{})
    if !ok {
        return 0
    }
    
    totalCount, ok := deploymentsData["totalCount"].(float64)
    if !ok {
        return 0
    }
    
    return int(totalCount)
}
```

#### 7. Use the extraction in buildBasicRepoStats

**File**: `internal/ghapi/graphql_extract.go`

```go
// In buildBasicRepoStats function (around line 13):
func buildBasicRepoStats(org string, r map[string]interface{}, packageData map[string]output.PackageCounts, issueEventsCount int) output.RepoStats {
    // ... existing getTotalCount, getString helpers ...
    
    // Extract all fields
    // ... existing extractions ...
    deploymentsCount := extractDeployments(r)
    
    return output.RepoStats{
        Org:  org,
        Name: getString("name"),
        // ... existing fields ...
        Deployments: deploymentsCount,
    }
}
```

**OR** if it's an expensive field, add to the expensive field extractors:

```go
// In graphql_fetch.go, add to expensiveFieldMapping (around line 229):
var expensiveFieldMapping = []struct {
    enabled   func(DataFetchConfig) bool
    extractor expensiveFieldExtractor
}{
    // ... existing mappings ...
    {
        enabled: func(c DataFetchConfig) bool { return c.FetchDeployments },
        extractor: func(repo map[string]interface{}, stats *output.RepoStats) {
            stats.Deployments = extractDeployments(repo)
        },
    },
}
```

#### 8. Update documentation

**File**: `README.md` - Add to feature flags table:

```markdown
| `--no-deployments` | Deployment history | ~0 calls (GraphQL) | Deployment counts per repo |
```

#### 9. Test the feature

```bash
# Build
go build -o gh-stats

# Test with batching (GraphQL) - default mode
./gh-stats --org test-org --verbose

# Test with flag disabled
./gh-stats --org test-org --no-deployments --verbose

# Test in minimal mode (should skip if expensive)
./gh-stats --org test-org --minimal --verbose

# Verify JSON output
cat gh-stats-*.json | jq '.repos[0].deployments'

# Verify GraphQL query includes the field
./gh-stats --org test-org --dry-run --verbose 2>&1 | grep -A 50 "GraphQL query"
```

**Complete!** You've now added deployment count collection via GraphQL with batching support.

---

### Summary: REST vs GraphQL

**When to use REST**:
- Need detailed item-level data (webhooks, branches, specific configs)
- Paginating through lists
- Endpoints not available in GraphQL

**When to use GraphQL**:
- Counting items (`first: 0` with `totalCount`)
- Fetching multiple fields in single query
- Can be batched across repositories (up to 50 repos/query)

**Key differences**:
- REST: Uses `RunRESTCallback` for pagination, called per-repo in `GetEnhancedRepoData`
- GraphQL: Add to `getRepositoryFields`, batched via `GetBatchedRepoBasicDetails`

---

## Adding a New Feature Flag

Feature flags allow users to skip optional data collection to reduce API usage. See the complete examples above for the full flow.

**Quick Reference - All Steps**:

#### 1. Add CLI flag variable (`cmd/root.go` ~line 30)

```go
var (
    noFeature    bool
)
```

#### 2. Define the flag (`cmd/root.go` init() ~line 280)

```go
rootCmd.Flags().BoolVar(&noFeature, "no-feature", false, "Skip fetching feature data")
```

#### 3. Add to DataFetchConfig (`internal/ghapi/repos_types.go`)

```go
type DataFetchConfig struct {
    FetchFeature bool
}
```

#### 4. Wire up in config (`cmd/root.go` rootCmd.RunE ~line 100)

```go
// Minimal mode: enable all --no-* flags
if minimal {
    noFeature = true
}

// CLI overrides: explicit flags take precedence
if cmd.Flags().Changed("no-feature") {
    noFeature, _ = cmd.Flags().GetBool("no-feature")
}

// Build config: convert --no-* to Fetch*
config := stats.Config{
    DataFetchConfig: ghapi.DataFetchConfig{
        FetchFeature: !noFeature,
    },
}
```

#### 5. Use in collection code

**REST**: `internal/ghapi/repos_metadata.go`

```go
if fetchConfig.FetchFeature {
    if err := fetchFeature(ctx, org, repo, stats); err != nil {
        pterm.Warning.Printf("Failed to fetch feature for %s/%s: %v\n", org, repo, err)
    }
}
```

**GraphQL**: `internal/ghapi/graphql_queries.go`

```go
if config.FetchFeature {
    fields += `feature { totalCount }`
}
```

#### 6. Update help text (`cmd/root.go` usage template ~line 240)

Add to appropriate impact section:

```
      --no-feature        Skip fetching feature data
```

#### 7. Document in README

Add to feature flags table with API cost and description.

**See complete examples**: [REST Example](#rest-api-example-adding-webhook-count) | [GraphQL Example](#graphql-api-example-adding-deployment-count)

---

## Adding a New Output Field

When adding a new field to the output JSON:

**Step 1: Add to data model** (`internal/output/models.go`)

```go
type RepoData struct {
    // ... existing fields ...
    NewField int `json:"newField"`
}
```

**Step 2: Populate the field** (in data collection code)

```go
repoData.NewField = fetchedValue
```

**Step 3: Update JSON output documentation** (in `README.md`)

Add the field to the output format example with description.

**Step 4: Test JSON output**

```bash
# Run and verify JSON structure
gh stats --org test-org
cat gh-stats-2025-11-06.json | jq '.repos[0]'
```

## Testing Changes

### Manual Testing

```bash
# 1. Build the extension
go build -o gh-stats

# 2. Test with a small organization
./gh-stats --org small-test-org --verbose

# 3. Test with feature flags
./gh-stats --org test-org --no-webhooks --verbose

# 4. Test automatic resume functionality
# Interrupt with Ctrl-C, then re-run (resumes automatically)
./gh-stats --org test-org

# 5. Test error handling
./gh-stats --org nonexistent-org --verbose

# 6. Test minimal mode
./gh-stats --org test-org --minimal

# 7. Test dry-run mode
./gh-stats --org test-org --dry-run
```

### Unit Testing

```bash
# Run tests
go test ./...

# Run with race detector
go test -race ./...

# Run with coverage
go test -cover ./...
```

### Linting and Formatting

```bash
# Format code
go fmt ./...

# Run linters
go vet ./...
golangci-lint run
staticcheck ./...

# Check for race conditions
go build -race
```

## Best Practices

### Code Organization

1. **Keep API logic separate**: All GitHub API calls should be in `internal/ghapi/`
2. **Single responsibility**: Each function should do one thing well
3. **Error handling**: Use descriptive error messages with context
4. **Logging**: Use `pterm` for user-facing output, `log` for debugging

### Performance

1. **Use GraphQL for bulk data**: Fetch multiple fields in single query when possible
2. **Use GraphQL batching**: Process multiple repositories in a single query (see Pattern 5 below)
3. **Batch operations**: Group related API calls together
4. **Cache when possible**: Store frequently accessed data in memory
5. **Respect rate limits**: Track all API calls with `state.Get().IncrementAPICalls()`

### Error Handling

1. **Distinguish critical vs. non-critical**: Critical errors stop processing, warnings continue
2. **Provide context**: Include org/repo names in error messages
3. **Log appropriately**: Use `pterm.Error` for failures, `pterm.Warning` for recoverable issues
4. **Return structured errors**: Use `fmt.Errorf` with `%w` for error wrapping

### Concurrency

1. **Thread-safe operations**: Use mutexes for shared state
2. **Channel communication**: Prefer channels over shared memory
3. **Context propagation**: Pass `context.Context` for cancellation support
4. **Worker pools**: Use semaphores (buffered channels) for concurrency control
5. **Error collection**: Use `appendError(mu, errors, org, repo, err)` for thread-safe error aggregation

## Debugging Tips

### Enable Verbose Mode

```bash
gh stats --org test-org --verbose
```

Shows:
- Individual API calls being made
- Rate limit status updates
- Detailed error messages
- Progress for each repository

### Check Rate Limits Manually

```bash
gh api rate_limit
```

### Inspect JSON Output

```bash
# Pretty print JSON
cat gh-stats-2025-11-06.json | jq '.'

# Check specific repository
cat gh-stats-2025-11-06.json | jq '.repos[] | select(.repo == "my-repo")'

# Count total repos
cat gh-stats-2025-11-06.json | jq '.repos | length'
```

### Test with Small Dataset

Create a test organization with 2-3 repositories to quickly iterate on changes.

### Use Go Debugging Tools

```bash
# Add logging
log.Printf("Debug: org=%s, repo=%s, value=%v\n", org, repo, value)

# Use delve debugger
dlv debug -- --org test-org
```

## Additional Resources

- [GitHub REST API Documentation](https://docs.github.com/en/rest)
- [GitHub GraphQL API Documentation](https://docs.github.com/en/graphql)
- [Go Best Practices](https://go.dev/doc/effective_go)
- [Concurrency in Go](https://go.dev/blog/pipelines)

## Getting Help

- Check existing [GitHub Issues](https://github.com/mona-actions/gh-stats/issues)
- Review similar features in the codebase
- Ask questions in pull request discussions
- Reach out to maintainers for architecture guidance
