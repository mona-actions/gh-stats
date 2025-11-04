# GitHub Copilot Instructions for gh-stats

This file provides context to GitHub Copilot about the codebase architecture, patterns, and conventions to help generate appropriate code suggestions.

## Project Overview

`gh-stats` is a GitHub CLI extension written in Go that collects repository statistics using GitHub's REST and GraphQL APIs. The tool uses a worker pool pattern for concurrent processing with built-in rate limiting and resume functionality.

## Architecture Layers

### 1. CLI Layer (`cmd/`)
- Uses `cobra` framework for command structure
- Command definitions in `cmd/root.go` (single root command, no subcommands)
- Flag parsing and validation
- Config struct population from flags

### 2. Orchestration Layer (`internal/stats/`)
- Main coordinator: `processor.go`
- Worker pool: `concurrency.go` (default 3 workers)
- Input validation: `validation.go`
- Sequential organization processing
- Parallel repository processing

### 3. API Layer (`internal/ghapi/`)
- GraphQL queries: `graphql_*.go` (execution, batching, pagination, extraction)
- REST API calls: `repos_*.go`, `orgs_*.go`
- Rate limiting: `client_ratelimit.go`
- Retry logic: `client_retry.go`
- REST operations: `client_rest.go`
- Pagination: `client_pagination.go`
- Types: `client_types.go`, `repos_types.go`
- Always use `gh` CLI for API calls (not direct HTTP)

### 4. State Management (`internal/state/`)
- Global state tracking: `state.go`
- Rate limit monitoring (REST and GraphQL separate)
- Progress tracking with `pterm`
- Thread-safe operations with mutexes

### 5. Output Layer (`internal/output/`)
- Data models: `models.go`
- JSON serialization: `json.go`
- Incremental writes (atomic with temp file + rename)
- Caching for performance

## Code Patterns and Conventions

### Making API Calls

#### REST API Pattern
```go
func FetchSomeData(org, repo string) (DataType, error) {
    endpoint := fmt.Sprintf("repos/%s/%s/resource", org, repo)
    
    // Track API usage
    state.Get().IncrementAPICalls()
    
    // Use --include to get headers (rate limiting handled by execute functions)
    cmd := exec.Command("gh", "api", "--include", endpoint)
    cmd.Env = os.Environ()
    
    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr
    
    if err := cmd.Run(); err != nil {
        return DataType{}, fmt.Errorf("failed to fetch data for %s/%s: %w\nstderr: %s", 
            org, repo, err, stderr.String())
    }
    
    // Parse JSON response directly
    var result DataType
    if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
        return DataType{}, fmt.Errorf("failed to parse response: %w", err)
    }
    
    return result, nil
}
```

#### GraphQL Query Pattern
```go
// 1. Define query with pagination
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
                field(first: 0) {
                    totalCount
                }
            }
        }
    }
}
`

// 2. Use RunGraphQLPaginated helper
vars := map[string]interface{}{
    "org": orgName,
}

pages, err := ghapi.RunGraphQLPaginated(query, vars, extractPaginationFunc, tracker)
if err != nil {
    return fmt.Errorf("GraphQL query failed: %w", err)
}

// 3. Process all pages
for _, page := range pages {
    nodes := extractNodes(page)
    // Process nodes...
}
```

#### GraphQL Batching Pattern (PREFERRED for Repository Data)
```go
// Batch processing reduces API calls significantly (e.g., 46% reduction for minimal mode)
// Default batch size: 20 repos, configurable via --graphql-batch-size (1-50)

// 1. Collect repositories to batch
repos := []string{"repo1", "repo2", "repo3"}  // Up to 50 repos per batch

// 2. Define fetch config (base fields are automatically extracted for batching)
fetchConfig := ghapi.DataFetchConfig{
    FetchSettings:      true,
    FetchLanguages:     true,
    FetchTopics:        true,
    FetchPRsData:       false,  // Expensive, skipped in minimal mode
    FetchIssuesData:    false,  // Expensive, skipped in minimal mode
    FetchContributors:  false,  // Expensive, skipped in minimal mode
    // Base fields (Settings, Languages, Topics) are kept in batch
    // Expensive fields are excluded from batch and fetched per-repo if enabled
}

// 3. Fetch batch data (returns map of repo name -> basic data)
basicRepoDataMap, failedRepos, err := ghapi.GetBatchedRepoBasicDetails(ctx, org, repos, fetchConfig)
if err != nil {
    return fmt.Errorf("batch fetch failed: %w", err)
}

// 4. Process results (with expensive fields fetched individually if needed)
for repoName, basicData := range basicRepoDataMap {
    // ProcessRepoWithBasicData handles expensive fields automatically
    stats, err := ghapi.ProcessRepoWithBasicData(ctx, org, repoName, basicData, 
        packageData, teamAccessMap, fetchConfig)
    if err != nil {
        if config.Verbose {
            pterm.Warning.Printf("Failed to process %s/%s: %v\n", org, repoName, err)
        }
        continue
    }
    
    // Use stats data
    processRepoData(stats)
}

// 5. Handle failed repos from batch
for _, repoName := range failedRepos {
    pterm.Warning.Printf("Repo not found in batch: %s/%s\n", org, repoName)
}

// Performance: 36 repos with batch size 50 = 1 GraphQL call (vs 36 individual calls)
// API reduction: 46% fewer calls in minimal mode with batching enabled
```

#### When to Use Batching vs Individual Queries

**Use Batching (GetBatchedRepoBasicDetails + ProcessRepoWithBasicData):**
- Collecting data for multiple repositories in parallel
- Worker pool processing repository lists
- Minimal mode or selective field collection
- Want to optimize API call count
- Base fields (Settings, Languages, Topics) for many repos

**Use Individual Queries (GetRepoDetails):**
- Single repository detailed analysis
- Custom query shapes not fitting batch pattern
- Need full control over query structure

**Use Pagination (RunGraphQLPaginated):**
- Organization-level data (members, teams, projects)
- Need pagination control for large result sets
- Fetching repository lists

### Error Handling

#### Critical vs Non-Critical Errors
```go
// Critical error - stops processing
basicData, err := fetchBasicData(org, repo)
if err != nil {
    return fmt.Errorf("critical: failed to fetch basic data: %w", err)
}

// Non-critical error - log warning and continue
if !config.NoPackages {
    packages, err := fetchPackages(org, repo)
    if err != nil {
        if config.Verbose {
            pterm.Warning.Printf("Could not fetch packages for %s/%s: %v\n", 
                org, repo, err)
        }
    } else {
        basicData.Packages = packages
    }
}
```

#### Error Message Format
- Include org/repo context: `"failed to fetch X for %s/%s: %w"`
- Wrap errors: `fmt.Errorf("description: %w", err)`
- Include stderr in API errors: `"error: %w\nstderr: %s"`

#### Error Aggregation Helper
```go
// When collecting multiple optional fields, aggregate errors
func appendError(existingErr error, newErr error, context string) error {
    if newErr == nil {
        return existingErr
    }
    errMsg := fmt.Sprintf("%s: %v", context, newErr)
    if existingErr == nil {
        return fmt.Errorf(errMsg)
    }
    return fmt.Errorf("%w; %s", existingErr, errMsg)
}

// Usage example:
var aggregatedErr error
if data1, err := fetchOptionalData1(); err != nil {
    aggregatedErr = appendError(aggregatedErr, err, "data1")
} else {
    result.Data1 = data1
}
if data2, err := fetchOptionalData2(); err != nil {
    aggregatedErr = appendError(aggregatedErr, err, "data2")
} else {
    result.Data2 = data2
}

// Return aggregate error if any optional fields failed
return result, aggregatedErr
```

### Rate Limiting

#### Track API Calls
```go
state.Get().IncrementAPICalls()
```

#### Rate Limiting is Built-In
Rate limiting is handled automatically by the execute functions (`executeGraphQLPage`, `executeRESTPaginatedCall`).
No manual rate limiter calls needed - just track API usage with `IncrementAPICalls()`.

### Feature Flags

#### Adding New Feature Flag
```go
// 1. Add flag in cmd/root.go
rootCmd.Flags().Bool("no-feature", false, "Skip fetching feature data")

// 2. Add to DataFetchConfig in repos_types.go
type DataFetchConfig struct {
    FetchFeature bool
}

// 3. Use in collection logic
if fetchConfig.FetchFeature {
    data, err := fetchFeature(org, repo)
    // handle...
}
```

### Data Models

#### JSON Tags Required
```go
type RepoData struct {
    Org       string `json:"org"`
    Repo      string `json:"repo"`
    Stars     int    `json:"stars"`
    // Always use json tags for output fields
}
```

#### Field Naming Convention
- Use camelCase for JSON fields
- Use PascalCase for Go struct fields
- Be descriptive but concise

### Concurrency

#### Worker Pool Pattern
```go
// Use buffered channel as semaphore
sem := make(chan struct{}, maxWorkers)
errChan := make(chan error, len(items))

for _, item := range items {
    sem <- struct{}{}  // Acquire
    go func(item Item) {
        defer func() { <-sem }()  // Release
        
        if err := processItem(item); err != nil {
            errChan <- err
        }
    }(item)
}

// Wait for completion
for i := 0; i < cap(sem); i++ {
    sem <- struct{}{}
}
```

#### Thread-Safe State Updates
```go
// Use mutexes for shared state
mu.Lock()
sharedState.Field = value
mu.Unlock()

// Or use channels for communication
progressUpdates := make(chan ProgressUpdate, bufferSize)
```

### Logging and Output

#### User-Facing Output (pterm)
```go
pterm.Success.Println("✓ Operation completed")
pterm.Warning.Printf("⚠ Could not fetch %s\n", resource)
pterm.Error.Printf("✗ Failed: %v\n", err)
pterm.Info.Printf("Processing %d items...\n", count)
```

#### Debug Output (verbose mode)
```go
if config.Verbose {
    pterm.Debug.Printf("Fetching data from %s\n", endpoint)
}
```

#### Never use fmt.Println for user output
Use `pterm` for all user-facing messages.

### File Operations

#### Atomic Writes Pattern
```go
// 1. Write to temp file
tempFile := outputFile + ".tmp"
f, err := os.Create(tempFile)
// write data...
f.Sync()  // Ensure written to disk
f.Close()

// 2. Atomic rename
if err := os.Rename(tempFile, outputFile); err != nil {
    return fmt.Errorf("failed to finalize output: %w", err)
}
```

#### JSON Operations
```go
// Use json.Marshal with proper indentation
data, err := json.MarshalIndent(output, "", "  ")
if err != nil {
    return fmt.Errorf("failed to marshal JSON: %w", err)
}
```

## Important Conventions

### 1. Rate Limiting is Mandatory
- **ALWAYS** track with `state.Get().IncrementAPICalls()`
- Rate limiting is built into execute functions (no manual calls needed)
- Secondary rate limits are handled automatically with retry logic

### 2. Error Context is Critical
- Include org/repo names in error messages
- Wrap errors with `%w` for error chains
- Include stderr output for command failures

### 3. Graceful Degradation
- Non-critical data failures should warn, not fail
- Feature flags allow skipping optional data
- Resume functionality requires incremental writes

### 4. Performance Considerations
- Use GraphQL for bulk operations (fewer API calls)
- Use `first: 0` in GraphQL to get counts without data
- Cache frequently accessed data in memory
- Write output incrementally (per repository)
- **Use GraphQL batching for repository data** (20 repos/batch by default)
- Batch size is configurable via `--graphql-batch-size` (1-50)
- Split config into base (cheap) vs expensive fields for optimal batching

### 5. Code Style
- Run `go fmt ./...` before committing
- Use descriptive variable names (no single letters except loop indices)
- Add godoc comments to exported functions
- Keep functions focused and under 50 lines when possible

### 6. Testing Requirements
- Add tests for new functionality
- Run `go test -race ./...` to check for race conditions
- Test with verbose mode for debugging
- Test resume functionality for data collection features

## Common Anti-Patterns to Avoid

❌ **Don't make direct HTTP requests** - Always use `gh` CLI  
❌ **Don't ignore rate limiting** - Every API call must be rate limited  
❌ **Don't use fmt.Println** - Use pterm for user output  
❌ **Don't hardcode values** - Use constants or config  
❌ **Don't panic** - Return errors with context  
❌ **Don't block without timeout** - Use context for cancellation  
❌ **Don't modify files in-place** - Use atomic writes (temp + rename)  
❌ **Don't share state without synchronization** - Use mutexes or channels  
❌ **Don't forget line ending normalization** - HTTP responses may have \r\n  
❌ **Don't use individual GraphQL queries for multiple repos** - Use batching instead  

## File Organization

### When adding new functionality:

1. **New REST API endpoint** → `internal/ghapi/repos_*.go` or `orgs_*.go`
2. **New GraphQL query** → `internal/ghapi/graphql_*.go`
3. **New data model** → `internal/output/models.go`
4. **New feature flag** → `cmd/root.go` (flag) + `internal/ghapi/repos_types.go` (DataFetchConfig)
5. **New command** → Not applicable (single root command architecture)

### Package responsibilities:
- `cmd/` - CLI interface only, no business logic
- `ghapi/` - All GitHub API interaction, no business logic
- `stats/` - Orchestration and processing logic
- `state/` - Global state and tracking only
- `output/` - Data models and serialization only

## Dependencies

- **cobra** - CLI framework (don't add alternatives)
- **pterm** - Terminal output (don't use other print libraries)
- **go-ratelimit** - Rate limiting (centralized in client_ratelimit.go)
- **gh CLI** - GitHub API access (required, don't replace)

## Testing Patterns

```go
// Test with actual API calls (integration tests)
func TestFetchData(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration test")
    }
    
    data, err := FetchData("test-org", "test-repo")
    if err != nil {
        t.Fatalf("Failed to fetch data: %v", err)
    }
    
    if data.Field == "" {
        t.Error("Expected field to be populated")
    }
}

// Run integration tests: go test -v ./...
// Skip integration tests: go test -short ./...
```

## Documentation Requirements

When adding features:
1. ✅ Add godoc comment to exported functions
2. ✅ Update README.md if user-facing
3. ✅ Add to feature flags table if applicable
4. ✅ Update DEVELOPER_GUIDE.md for new patterns
5. ✅ Update output format documentation if changing JSON structure

## Quick Reference

### Key Files to Know
- `cmd/root.go` - Command definition and flags
- `internal/stats/processor.go` - Main orchestration logic
- `internal/stats/concurrency.go` - Worker pool implementation
- `internal/ghapi/client_ratelimit.go` - Rate limiting
- `internal/ghapi/graphql_*.go` - GraphQL query execution and batching
- `internal/output/json.go` - JSON output with caching
- `internal/state/state.go` - Global state management

### Common Tasks Quick Links
- Add REST API call → See `internal/ghapi/repos_*.go` examples
- Add GraphQL field → See `internal/ghapi/graphql_*.go` query patterns
- Add feature flag → See `cmd/root.go` flag definitions
- Add output field → See `internal/output/models.go` structs
- Debug rate limits → Use `--verbose` flag
- Test changes → `go test -race ./...` and manual testing
- Optimize API calls → Use GraphQL batching pattern (see `GetBatchedRepoBasicDetails`)
- Parse HTTP responses → Always use `extractHeadersAndBody()` for line ending normalization

---

**Remember**: This is a production tool used by many organizations. Prioritize reliability, rate limit compliance, and data integrity over performance optimizations.

## Recent Improvements & Patterns to Follow

### GraphQL Batching Implementation (v0.2.0)
- **Pattern**: Batch up to 50 repositories per GraphQL query
- **Performance**: 46% fewer API calls in minimal mode (verified with 36 repos)
- **Configuration**: `--graphql-batch-size` flag (default: 20)
- **Implementation**: See `internal/ghapi/graphql_batching.go` - `GetBatchedRepoBasicDetails()`
- **When to use**: Worker pool processing, minimal mode, selective field collection

### Config Splitting Pattern
```go
// The batching implementation automatically extracts base fields
// Base fields: Settings, CustomProps (cheap to fetch in batch)
// Expensive fields: Collaborators, Rulesets, Milestones, etc. (fetched per-repo)

// Define fetch config with all fields
fetchConfig := ghapi.DataFetchConfig{
    FetchSettings:      true,  // Always fetched in batch
    FetchCustomProps:   true,  // Always fetched in batch
    FetchLanguages:     true,  // Can be batched
    FetchTopics:        true,  // Can be batched
    FetchPRsData:       config.FetchPRsData,       // Fetched per-repo if true
    FetchIssuesData:    config.FetchIssuesData,    // Fetched per-repo if true
    FetchContributors:  config.FetchContributors,  // Fetched per-repo if true
    FetchCollaborators: config.FetchCollaborators, // Expensive, per-repo
    // ... other expensive fields
}

// GetBatchedRepoBasicDetails internally uses getBaseOnlyConfig() to extract base fields
// ProcessRepoWithBasicData fetches expensive fields individually if needed
```

### Minimal Mode Override Pattern
```go
// When minimal flag is set, enable all --no-* flags first
if minimal {
    noContributors = true
    noPRsData = true
    // ... all other no-* flags
}

// Then apply CLI overrides (explicit flags take precedence)
if cmd.Flags().Changed("no-packages") {
    noPackages, _ = cmd.Flags().GetBool("no-packages")
}

// Convert to DataFetchConfig (negative flags → positive flags)
DataFetchConfig: ghapi.DataFetchConfig{
    FetchContributors: !noContributors,
    FetchPRsData:      !noPRsData,
    // ... etc
}

// Result: --minimal --no-packages=false fetches packages despite minimal mode
```

### New Helper Functions
- `appendError(mu, errors, org, repo, err)` - Thread-safe error collection (in concurrency.go)
- `GetBatchedRepoBasicDetails(ctx, org, repos, fetchConfig)` - Batch fetch repository base data (in graphql_batching.go)
- `ProcessRepoWithBasicData(ctx, org, repo, basicData, packages, teams, fetchConfig)` - Process repo with pre-fetched batch data (in repos_metadata.go)
- `getBaseOnlyConfig(config)` - Extract only base fields from config (internal, in graphql_batching.go)
- `getExpensiveOnlyConfig(config)` - Extract only expensive fields from config (internal, in graphql_batching.go)
