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
│ - GraphQL queries (graphql.go)                      │
│ - REST API calls (repositories.go, organizations.go)│
│ - Rate limiting & retry logic (client.go)           │
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

### REST API Example

Let's add a new REST API call to fetch repository webhooks count.

**Step 1: Define the response structure** (if needed)

Add to `internal/ghapi/client.go` or create a new file:

```go
// WebhookResponse represents a webhook from the GitHub API
type WebhookResponse struct {
    ID        int64  `json:"id"`
    Name      string `json:"name"`
    Active    bool   `json:"active"`
    CreatedAt string `json:"created_at"`
    UpdatedAt string `json:"updated_at"`
}
```

**Step 2: Create the API call function**

Add to `internal/ghapi/repositories.go`:

```go
// FetchWebhooks fetches all webhooks for a repository using the REST API.
// Returns webhook count and any error encountered.
func FetchWebhooks(org, repo string) (int, error) {
    // Build the API endpoint
    endpoint := fmt.Sprintf("repos/%s/%s/hooks", org, repo)
    
    // Create a client and update rate limits
    client := NewClient()
    
    // Use the global rate limiter
    client.Take()
    
    // Increment API call counter
    state.GlobalState.IncrementAPICalls()
    
    // Execute the API call with --include to get headers
    cmd := exec.Command("gh", "api", "--include", endpoint)
    cmd.Env = os.Environ()
    
    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr
    
    if err := cmd.Run(); err != nil {
        return 0, fmt.Errorf("failed to fetch webhooks for %s/%s: %w\nstderr: %s", 
            org, repo, err, stderr.String())
    }
    
    // Parse headers and update rate limiter
    response := stdout.String()
    headers, body := ExtractHeadersAndBody(response)
    client.UpdateLimiterFromHeaders(headers)
    
    // Parse the JSON response
    var webhooks []WebhookResponse
    if err := json.Unmarshal([]byte(body), &webhooks); err != nil {
        return 0, fmt.Errorf("failed to parse webhooks response: %w", err)
    }
    
    return len(webhooks), nil
}
```

**Step 3: Integrate into data collection**

Add to `internal/stats/processor.go` in the `collectRepoData` function:

```go
// Fetch webhooks if not disabled
var webhooksCount int
if !config.NoWebhooks {
    count, err := ghapi.FetchWebhooks(org, repo)
    if err != nil {
        if config.Verbose {
            pterm.Warning.Printf("Could not fetch webhooks for %s/%s: %v\n", org, repo, err)
        }
    } else {
        webhooksCount = count
    }
}
```

**Step 4: Add to data model**

Update `internal/output/models.go`:

```go
type RepoData struct {
    // ... existing fields ...
    Webhooks int `json:"webhooks"`
}
```

**Step 5: Update the feature flag** (see [Adding a New Feature Flag](#adding-a-new-feature-flag))

### GraphQL API Example

Let's add a new GraphQL field to fetch deployment count.

**Step 1: Update the GraphQL query**

In `internal/ghapi/graphql.go`, modify the `buildRepositoryQuery` function:

```go
const query = `
query($org: String!, $endCursor: String) {
    organization(login: $org) {
        repositories(first: 50, after: $endCursor) {
            pageInfo {
                endCursor
                hasNextPage
            }
            nodes {
                name
                isArchived
                isFork
                # ... existing fields ...
                
                # Add the new field
                deployments(first: 0) {
                    totalCount
                }
            }
        }
    }
}
`
```

**Step 2: Extract the field in processing**

Update `internal/ghapi/graphql.go` in the data extraction section:

```go
func processGraphQLRepoData(repoNode map[string]interface{}) RepoData {
    // ... existing field extraction ...
    
    // Extract deployment count
    deploymentsCount := 0
    if deployments, ok := repoNode["deployments"].(map[string]interface{}); ok {
        if count, ok := deployments["totalCount"].(float64); ok {
            deploymentsCount = int(count)
        }
    }
    
    return RepoData{
        // ... existing fields ...
        Deployments: deploymentsCount,
    }
}
```

**Step 3: Add to data model**

Update `internal/output/models.go`:

```go
type RepoData struct {
    // ... existing fields ...
    Deployments int `json:"deployments"`
}
```

### Important Considerations for API Calls

1. **Always use rate limiting**: Call `client.Take()` before making API requests
2. **Track API usage**: Call `state.GlobalState.IncrementAPICalls()` for each call
3. **Update rate limiter**: Use `client.UpdateLimiterFromHeaders(headers)` after responses
4. **Handle errors gracefully**: Log warnings for non-critical data, continue processing
5. **Use --include flag**: Get headers with `gh api --include` to track rate limits
6. **Test with verbose mode**: Ensure proper logging with `--verbose` flag

## Adding a New Feature Flag

Feature flags allow users to skip optional data collection to reduce API usage.

**Step 1: Add flag to command definition**

In `cmd/run.go`, add the flag:

```go
func init() {
    // ... existing flags ...
    runCmd.Flags().Bool("no-webhooks", false, "Skip fetching webhook information")
}
```

**Step 2: Add to config struct**

In `internal/stats/processor.go` (or wherever config is defined):

```go
type Config struct {
    // ... existing fields ...
    NoWebhooks bool
}
```

**Step 3: Read flag value**

In `cmd/run.go` where config is populated:

```go
config := &stats.Config{
    // ... existing fields ...
    NoWebhooks: viper.GetBool("no-webhooks"),
}
```

**Step 4: Use in data collection**

In `internal/stats/processor.go`:

```go
// Only fetch webhooks if not disabled
if !config.NoWebhooks {
    webhooks, err := ghapi.FetchWebhooks(org, repo)
    // ... handle result ...
}
```

**Step 5: Update documentation**

Add to the feature flags table in `README.md`:

```markdown
| `--no-webhooks` | Webhook configurations | ~1 call/repo | Skip webhook details |
```

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
gh stats run --org test-org
cat gh-stats-2025-10-30.json | jq '.repos[0]'
```

## Testing Changes

### Manual Testing

```bash
# 1. Build the extension
go build -o gh-stats

# 2. Test with a small organization
./gh-stats run --org small-test-org --verbose

# 3. Test with feature flags
./gh-stats run --org test-org --no-webhooks --verbose

# 4. Test resume functionality
# Interrupt with Ctrl-C, then re-run
./gh-stats run --org test-org

# 5. Test error handling
./gh-stats run --org nonexistent-org --verbose
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
2. **Batch operations**: Group related API calls together
3. **Cache when possible**: Store frequently accessed data in memory
4. **Respect rate limits**: Always use the global rate limiter

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

## Common Patterns

### Pattern 1: Adding a REST API Call with Pagination

```go
func FetchPaginatedData(org, repo string) ([]Item, error) {
    endpoint := fmt.Sprintf("repos/%s/%s/items", org, repo)
    
    client := NewClient()
    var allItems []Item
    page := 1
    
    for {
        client.Take()
        state.GlobalState.IncrementAPICalls()
        
        pagedEndpoint := fmt.Sprintf("%s?page=%d&per_page=100", endpoint, page)
        cmd := exec.Command("gh", "api", "--include", pagedEndpoint)
        
        // ... execute command and parse response ...
        
        if len(items) == 0 {
            break // No more pages
        }
        
        allItems = append(allItems, items...)
        page++
    }
    
    return allItems, nil
}
```

### Pattern 2: Adding a GraphQL Field with Error Handling

```go
func extractFieldSafely(node map[string]interface{}, field string) int {
    if fieldData, ok := node[field].(map[string]interface{}); ok {
        if count, ok := fieldData["totalCount"].(float64); ok {
            return int(count)
        }
    }
    return 0 // Return default if field not found
}

// Usage:
deployments := extractFieldSafely(repoNode, "deployments")
```

### Pattern 3: Conditional Data Collection with Feature Flags

```go
func collectRepoData(org, repo string, config *Config) (*RepoData, error) {
    data := &RepoData{
        Org:  org,
        Repo: repo,
    }
    
    // Always collect basic data
    data.Stars = fetchStars(org, repo)
    
    // Conditional collection based on flags
    if !config.NoWebhooks {
        data.Webhooks = fetchWebhooks(org, repo)
    }
    
    if !config.NoActions {
        data.Workflows = fetchWorkflows(org, repo)
    }
    
    return data, nil
}
```

### Pattern 4: Graceful Error Handling in Workers

```go
func processRepository(org, repo string, config *Config) error {
    // Attempt to fetch critical data
    basicData, err := fetchBasicData(org, repo)
    if err != nil {
        return fmt.Errorf("critical: failed to fetch basic data: %w", err)
    }
    
    // Attempt to fetch optional data
    if !config.NoPackages {
        packages, err := fetchPackages(org, repo)
        if err != nil {
            // Log warning but continue
            if config.Verbose {
                pterm.Warning.Printf("Could not fetch packages for %s/%s: %v\n", 
                    org, repo, err)
            }
        } else {
            basicData.Packages = packages
        }
    }
    
    return saveData(basicData)
}
```

## Debugging Tips

### Enable Verbose Mode

```bash
gh stats run --org test-org --verbose
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
cat gh-stats-2025-10-30.json | jq '.'

# Check specific repository
cat gh-stats-2025-10-30.json | jq '.repos[] | select(.repo == "my-repo")'

# Count total repos
cat gh-stats-2025-10-30.json | jq '.repos | length'
```

### Test with Small Dataset

Create a test organization with 2-3 repositories to quickly iterate on changes.

### Use Go Debugging Tools

```bash
# Add logging
log.Printf("Debug: org=%s, repo=%s, value=%v\n", org, repo, value)

# Use delve debugger
dlv debug -- run --org test-org
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
