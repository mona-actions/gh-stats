# GitHub Copilot Instructions for gh-stats

This file provides context to GitHub Copilot about the codebase architecture, patterns, and conventions to help generate appropriate code suggestions.

## Project Overview

`gh-stats` is a GitHub CLI extension written in Go that collects repository statistics using GitHub's REST and GraphQL APIs. The tool uses a worker pool pattern for concurrent processing with built-in rate limiting and resume functionality.

## Architecture Layers

### 1. CLI Layer (`cmd/`)
- Uses `cobra` framework for command structure
- Command definitions in `cmd/run.go`
- Flag parsing and validation
- Config struct population from flags

### 2. Orchestration Layer (`internal/stats/`)
- Main coordinator: `processor.go`
- Worker pool: `concurrency.go` (default 3 workers)
- Input validation: `validation.go`
- Sequential organization processing
- Parallel repository processing

### 3. API Layer (`internal/ghapi/`)
- GraphQL queries: `graphql.go`
- REST API calls: `repositories.go`, `organizations.go`
- Rate limiting & retry: `client.go`
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

**IMPORTANT**: Always use `BuildGHCommand` helper instead of `exec.Command` directly. This ensures GHES support via `--hostname` flag.

#### REST API Pattern
```go
func FetchSomeData(ctx context.Context, org, repo string) (DataType, error) {
    endpoint := fmt.Sprintf("repos/%s/%s/resource", org, repo)
    
    // Use BuildGHCommand for automatic GHES hostname support
    cmd := ghapi.BuildGHCommand(ctx, "api", "--include", endpoint)
    cmd.Env = os.Environ()
    
    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr
    
    if err := cmd.Run(); err != nil {
        return DataType{}, fmt.Errorf("failed to fetch data for %s/%s: %w\nstderr: %s", 
            org, repo, err, stderr.String())
    }
    
    // Parse JSON response
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

### Rate Limiting

#### Always Use Global Rate Limiter
```go
client := ghapi.NewClient()
client.Take()  // Blocks until rate limit allows
```

#### Track All API Calls
```go
state.GlobalState.IncrementAPICalls()
// or
client.IncrementAPICall(tracker)
```

#### Update After Responses
```go
headers, body := ghapi.ExtractHeadersAndBody(response)
client.UpdateLimiterFromHeaders(headers)
```

### Feature Flags

#### Adding New Feature Flag
```go
// 1. Add flag in cmd/run.go
runCmd.Flags().Bool("no-feature", false, "Skip fetching feature data")

// 2. Add to Config struct
type Config struct {
    NoFeature bool
}

// 3. Use in collection logic
if !config.NoFeature {
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
- **ALWAYS** call `client.Take()` before API calls
- **ALWAYS** track with `IncrementAPICalls()`
- **ALWAYS** update limiter from response headers

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

## File Organization

### When adding new functionality:

1. **New API endpoint** → `internal/ghapi/repositories.go` or `organizations.go`
2. **New GraphQL query** → `internal/ghapi/graphql.go`
3. **New data model** → `internal/output/models.go`
4. **New feature flag** → `cmd/run.go` (flag) + `internal/stats/processor.go` (config)
5. **New command** → `cmd/` (new file)

### Package responsibilities:
- `cmd/` - CLI interface only, no business logic
- `ghapi/` - All GitHub API interaction, no business logic
- `stats/` - Orchestration and processing logic
- `state/` - Global state and tracking only
- `output/` - Data models and serialization only

## Dependencies

- **cobra** - CLI framework (don't add alternatives)
- **pterm** - Terminal output (don't use other print libraries)
- **go-ratelimit** - Rate limiting (centralized in client.go)
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
- `cmd/run.go` - Command definition and flags
- `internal/stats/processor.go` - Main orchestration logic
- `internal/stats/concurrency.go` - Worker pool implementation
- `internal/ghapi/client.go` - Rate limiting and API client
- `internal/ghapi/graphql.go` - GraphQL query execution
- `internal/output/json.go` - JSON output with caching
- `internal/state/state.go` - Global state management

### Common Tasks Quick Links
- Add REST API call → See `internal/ghapi/repositories.go` examples
- Add GraphQL field → See `internal/ghapi/graphql.go` query patterns
- Add feature flag → See `cmd/run.go` flag definitions
- Add output field → See `internal/output/models.go` structs
- Debug rate limits → Use `--verbose` flag
- Test changes → `go test -race ./...` and manual testing

---

**Remember**: This is a production tool used by many organizations. Prioritize reliability, rate limit compliance, and data integrity over performance optimizations.
