// Package ghapi provides functions for interacting with the GitHub API (GraphQL and REST).
package ghapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/mona-actions/gh-stats/internal/output"
	"github.com/mona-actions/gh-stats/internal/state"
	"github.com/pterm/pterm"
)

// BuildGHCommand constructs a gh CLI command with the proper hostname flag for GHES support.
// It inserts the --hostname flag right after "gh" and before other arguments.
//
// Example:
//
//	BuildGHCommand(ctx, "api", "repos/org/repo") -> ["gh", "--hostname", "ghes.example.com", "api", "repos/org/repo"]
//	BuildGHCommand(ctx, "api", "repos/org/repo") -> ["gh", "api", "repos/org/repo"] (when hostname is empty)
func BuildGHCommand(ctx context.Context, args ...string) *exec.Cmd {
	hostname := state.Get().GetHostname()

	if hostname != "" {
		// Insert --hostname flag after "gh" command
		fullArgs := make([]string, 0, len(args)+2)
		fullArgs = append(fullArgs, "--hostname", hostname)
		fullArgs = append(fullArgs, args...)
		return exec.CommandContext(ctx, "gh", fullArgs...)
	}

	return exec.CommandContext(ctx, "gh", args...)
}

// Package types supported by GitHub.
const (
	PackageTypeContainer = "container"
	PackageTypeNPM       = "npm"
	PackageTypeMaven     = "maven"
	PackageTypeRubyGems  = "rubygems"
	PackageTypeNuGet     = "nuget"
	PackageTypeDocker    = "docker"
)

// API request and retry configuration constants.
const (
	// maxRateLimitRetries is the maximum number of retries for secondary rate limit errors.
	// Secondary rate limits occur when GitHub's abuse detection triggers due to too many
	// requests in a short time. These require longer wait times (60s-900s) before retrying.
	// Value: 3 retries balances persistence with avoiding excessive waiting
	maxRateLimitRetries = 3

	// maxRetries is the maximum number of retries for transient server errors (HTTP 5xx).
	// Server errors like 504 Gateway Timeout, 502 Bad Gateway, 503 Service Unavailable
	// are typically temporary and use exponential backoff (1s, 2s, 4s, 8s...).
	// Value: 3 retries means up to 7s total wait time (1+2+4) before giving up
	maxRetries = 3

	// maxBackoffDuration caps exponential backoff to prevent excessive wait times
	// Value: 30s is reasonable max for user-facing tool without appearing hung
	maxBackoffDuration = 30 * time.Second

	retryBackoffBaseDuration = 1 * time.Second // Base duration for exponential backoff (doubles each attempt: 1s, 2s, 4s, 8s...)
)

// Constants for configuration
const (
	MaxConcurrentPackageFetches = 2
	PackageTypeCycleInterval    = 500 * time.Millisecond
	DoneMessageDisplayTime      = 1200 * time.Millisecond
	minRetryWait                = 60 * time.Second
	maxRetryWait                = 15 * time.Minute
)

// SupportedPackageTypes lists all supported package types for the GitHub Packages API.
var SupportedPackageTypes = []string{
	PackageTypeContainer,
	PackageTypeNPM,
	PackageTypeMaven,
	PackageTypeRubyGems,
	PackageTypeNuGet,
	PackageTypeDocker,
}

// parseRetryAfter parses the Retry-After HTTP header value.
//
// The header can be either:
//   - An integer representing seconds to wait
//   - An HTTP date (RFC1123 format) representing absolute retry time
//
// Returns the duration to wait before retrying. Falls back to minRetryWait (60s)
// if parsing fails.
func parseRetryAfter(value string) time.Duration {
	// Try parsing as integer (seconds)
	var seconds int
	if _, err := fmt.Sscanf(value, "%d", &seconds); err == nil {
		return time.Duration(seconds) * time.Second
	}

	// Try parsing as HTTP date
	if t, err := time.Parse(time.RFC1123, value); err == nil {
		return time.Until(t)
	}

	// Default to 1 minute if parsing fails
	return minRetryWait
}

// parseLinkHeader extracts the page number from a GitHub Link header for the specified rel type.
//
// GitHub uses Link headers for pagination in REST API responses. This function parses
// headers like: <https://api.github.com/repos/org/repo/commits?per_page=1&page=966>; rel="last"
//
// Parameters:
//   - linkHeader: The value of the Link HTTP header
//   - rel: The relationship type to find (e.g., "last", "next", "prev", "first")
//
// Returns:
//   - page number and true if the specified rel was found
//   - 0 and false if not found or parsing failed
func parseLinkHeader(linkHeader string, rel string) (int, bool) {
	// Split by comma to get individual links
	links := strings.Split(linkHeader, ",")

	// Look for the rel we want (e.g., "last", "next")
	relPattern := fmt.Sprintf(`rel="%s"`, rel)
	for _, link := range links {
		if strings.Contains(link, relPattern) {
			// Extract the URL part between < and >
			start := strings.Index(link, "<")
			end := strings.Index(link, ">")
			if start == -1 || end == -1 {
				continue
			}

			url := link[start+1 : end]

			// Extract page parameter
			parts := strings.Split(url, "?")
			if len(parts) < 2 {
				continue
			}

			// Parse query parameters
			params := strings.Split(parts[1], "&")
			for _, param := range params {
				keyValue := strings.Split(param, "=")
				if len(keyValue) == 2 && keyValue[0] == "page" {
					var pageNum int
					if _, err := fmt.Sscanf(keyValue[1], "%d", &pageNum); err == nil {
						return pageNum, true
					}
				}
			}
		}
	}

	return 0, false
}

// getCountFromLinkHeader efficiently determines the total count of items by making a minimal API call.
//
// This function requests only 1 item per page (per_page=1) and parses the Link header's
// rel="last" to determine the total count. This is more efficient than fetching all items
// when you only need the count.
//
// Returns:
//   - Total count of items
//   - Error if the API call fails or headers cannot be parsed
//
// Note: If no Link header is present, the count is 0 or 1 (determined by parsing response body).
func getCountFromLinkHeader(ctx context.Context, endpoint string) (int, error) {
	if err := state.Get().CheckRateLimit(1); err != nil {
		return 0, fmt.Errorf("rate limit check failed: %w", err)
	}

	// Add per_page=1 to the endpoint
	separator := "?"
	if strings.Contains(endpoint, "?") {
		separator = "&"
	}
	endpoint = endpoint + separator + "per_page=1"

	var lastErr error
	for attempt := 0; attempt < maxRateLimitRetries; attempt++ {
		// Check for context cancellation before retry (but not on first attempt)
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			default:
			}
		}

		cmd := BuildGHCommand(ctx, "api", "--include", endpoint)
		cmd.Env = os.Environ()

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			lastErr = fmt.Errorf("failed to fetch count from Link header: %w (stderr: %s)", err, stderr.String())

			// Check for secondary rate limit
			if shouldRetry, waitTime := handleSecondaryRateLimit(stderr.String(), nil, attempt); shouldRetry {
				pterm.Warning.Printf("⚠ Secondary rate limit hit for %s. Waiting %v (attempt %d/%d)\n",
					endpoint, waitTime, attempt+1, maxRateLimitRetries)
				time.Sleep(waitTime)
				continue
			}

			return 0, lastErr
		}

		state.Get().IncrementAPICalls()

		// Parse headers from the response
		output := stdout.String()
		lines := strings.Split(output, "\n")

		var linkHeader string
		for _, line := range lines {
			if strings.HasPrefix(strings.ToLower(line), "link:") {
				linkHeader = strings.TrimPrefix(line, "Link:")
				linkHeader = strings.TrimPrefix(linkHeader, "link:")
				linkHeader = strings.TrimSpace(linkHeader)
				break
			}
		}

		if linkHeader == "" {
			// No Link header means either 0 or 1 result
			// Parse the JSON body to count items
			// Find the JSON part (after headers)
			jsonStart := strings.Index(output, "[")
			if jsonStart == -1 {
				return 0, nil // No items
			}

			jsonBody := output[jsonStart:]
			var items []interface{}
			if err := json.Unmarshal([]byte(jsonBody), &items); err != nil {
				return 0, fmt.Errorf("failed to parse response: %w", err)
			}
			return len(items), nil
		}

		// Parse the Link header for rel="last"
		if lastPage, found := parseLinkHeader(linkHeader, "last"); found {
			return lastPage, nil
		}

		// No rel="last" means we're on the last page
		// Parse rel="next" to see if there's a next page
		if nextPage, found := parseLinkHeader(linkHeader, "next"); found {
			// If next is page 2, we have at least 2 pages
			// But without rel="last", we can't determine exact count
			// Return next page number as minimum count
			return nextPage, nil
		}

		// Only one page of results
		return 1, nil
	}

	return 0, lastErr
}

// detectSecondaryRateLimit checks if an error is a secondary rate limit.
// Returns the wait duration and true if a secondary rate limit is detected.
func detectSecondaryRateLimit(stderr string, headers map[string]string) (time.Duration, bool) {
	stderrLower := strings.ToLower(stderr)

	// Check stderr for rate limit indicators
	isRateLimit := strings.Contains(stderrLower, "rate limit") ||
		strings.Contains(stderrLower, "429") ||
		strings.Contains(stderrLower, "abuse")

	if !isRateLimit {
		return 0, false
	}

	// Check for retry-after header (case-insensitive)
	for name, value := range headers {
		if strings.ToLower(name) == "retry-after" {
			duration := parseRetryAfter(value)
			return duration, true
		}
	}

	// Default to minimum retry wait if no retry-after header
	return minRetryWait, true
}

// handleSecondaryRateLimit determines if we should retry and how long to wait.
// Returns shouldRetry and waitDuration.
func handleSecondaryRateLimit(stderr string, headers map[string]string, attempt int) (bool, time.Duration) {
	waitTime, isRateLimit := detectSecondaryRateLimit(stderr, headers)

	if !isRateLimit {
		return false, 0
	}

	// Don't retry if we've exceeded max attempts
	if attempt >= maxRateLimitRetries {
		return false, 0
	}

	// Use exponential backoff if no retry-after header was provided
	if waitTime == minRetryWait {
		waitTime = time.Duration(math.Pow(2, float64(attempt))) * minRetryWait
		if waitTime > maxRetryWait {
			waitTime = maxRetryWait
		}
	}

	return true, waitTime
}

// GraphQLPageFunc is a callback that should return the endCursor and hasNextPage from the current response.
type GraphQLPageFunc func(data map[string]interface{}) (string, bool)

// PackageResponse is the REST API response structure for a package.
// This struct matches the GitHub Packages API response format.
type PackageResponse struct {
	Name       string `json:"name"`         // Package name
	Type       string `json:"package_type"` // Package type (npm, maven, etc.)
	Visibility string `json:"visibility"`   // Package visibility (public, private)
	CreatedAt  string `json:"created_at"`   // ISO 8601 timestamp
	UpdatedAt  string `json:"updated_at"`   // ISO 8601 timestamp
	Repository struct {
		Name string `json:"name"` // Associated repository name (empty if unassigned)
	} `json:"repository"`
	VersionCount *int `json:"version_count,omitempty"` // Number of versions (nil if not available from API)
}

// RateLimitResponse represents the GitHub API rate limit response.
// This struct matches the GitHub API rate limit endpoint response format.
type RateLimitResponse struct {
	Resources struct {
		Core struct {
			Limit     int64 `json:"limit"`     // Total API calls allowed per hour
			Remaining int64 `json:"remaining"` // API calls remaining in current hour
			Reset     int64 `json:"reset"`     // Unix timestamp when rate limit resets
		} `json:"core"`
		GraphQL struct {
			Limit     int64 `json:"limit"`     // Total GraphQL points allowed per hour
			Used      int64 `json:"used"`      // GraphQL points used in current hour
			Remaining int64 `json:"remaining"` // GraphQL points remaining in current hour
			Reset     int64 `json:"reset"`     // Unix timestamp when rate limit resets
		} `json:"graphql"`
	} `json:"resources"`
}

// RunGraphQLPaginated executes a paginated GraphQL query using the GitHub CLI,
// accumulating all pages of results. The pageFunc callback extracts pagination info
// from each response. Returns a slice of all page results.
// executeGraphQLPage executes a single GraphQL page with retries
func executeGraphQLPage(ctx context.Context, query string, variables map[string]interface{}, endCursor string, pageCount int) (map[string]interface{}, error) {
	var lastErr error

	for attempt := 0; attempt < maxRateLimitRetries; attempt++ {
		// Check for context cancellation before retry
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
		}

		args := []string{"api", "graphql"}
		args = append(args, "-f", fmt.Sprintf("query=%s", query))

		// Add variables with appropriate flags based on type
		for k, v := range variables {
			switch v.(type) {
			case int, int64, int32, float64, float32, bool:
				args = append(args, "-F", fmt.Sprintf("%s=%v", k, v))
			default:
				args = append(args, "-f", fmt.Sprintf("%s=%v", k, v))
			}
		}

		if endCursor != "" {
			args = append(args, "-f", fmt.Sprintf("endCursor=%s", endCursor))
		}

		cmd := BuildGHCommand(ctx, args...)
		cmd.Env = os.Environ()
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			lastErr = fmt.Errorf("GraphQL API call failed (page %d): %w\nstderr: %s", pageCount, err, stderr.String())

			// Check for secondary rate limit
			if shouldRetry, waitTime := handleSecondaryRateLimit(stderr.String(), nil, attempt); shouldRetry {
				pterm.Warning.Printf("⚠ Secondary rate limit hit on GraphQL page %d. Waiting %v (attempt %d/%d)\n",
					pageCount, waitTime, attempt+1, maxRateLimitRetries)
				time.Sleep(waitTime)
				continue
			}

			return nil, lastErr
		}

		state.Get().IncrementAPICalls()

		// Update rate limit info every 50 API calls
		if pageCount%50 == 0 {
			UpdateRateLimitInfo()
			state.Get().PrintRateLimit()
		}

		var result map[string]interface{}
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			return nil, fmt.Errorf("failed to parse GraphQL response (page %d): %w\nresponse: %s", pageCount, err, stdout.String())
		}

		// Check for GraphQL errors
		if errors, ok := result["errors"].([]interface{}); ok && len(errors) > 0 {
			return nil, fmt.Errorf("GraphQL errors on page %d: %v", pageCount, errors)
		}

		return result, nil
	}

	return nil, lastErr
}

func RunGraphQLPaginated(ctx context.Context, query string, variables map[string]interface{}, pageFunc GraphQLPageFunc) ([]map[string]interface{}, error) {
	var allPages []map[string]interface{}
	var endCursor string
	pageCount := 0

	// Update rate limit info at the start
	UpdateRateLimitInfo()

	// Check rate limit before starting
	if err := state.Get().CheckRateLimit(10); err != nil {
		return nil, fmt.Errorf("rate limit check failed: %w", err)
	}

	for {
		// Check for cancellation
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		pageCount++

		// Check rate limit every 10 pages
		if pageCount > 1 && pageCount%10 == 0 {
			UpdateRateLimitInfo()
			if err := state.Get().CheckRateLimit(5); err != nil {
				return nil, fmt.Errorf("rate limit check failed on page %d: %w", pageCount, err)
			}
		}

		result, err := executeGraphQLPage(ctx, query, variables, endCursor, pageCount)
		if err != nil {
			return nil, err
		}

		allPages = append(allPages, result)

		nextCursor, hasNextPage := pageFunc(result)
		if !hasNextPage {
			break
		}
		endCursor = nextCursor
	}

	return allPages, nil
}

// RunRESTCallback executes a REST API call with pagination and calls the callback for each page.
// This is a generic helper for REST pagination that processes data through a callback.
func RunRESTCallback(ctx context.Context, endpoint string, callback func([]byte) error) error {
	if err := state.Get().CheckRateLimit(5); err != nil {
		return fmt.Errorf("rate limit check failed: %w", err)
	}

	var lastErr error

	// Retry loop for secondary rate limits
	for attempt := 0; attempt < maxRateLimitRetries; attempt++ {
		// Check for cancellation before retry (but not on first attempt)
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}

		cmd := BuildGHCommand(ctx, "api", "--paginate", endpoint)
		cmd.Env = os.Environ()

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			lastErr = fmt.Errorf("REST API call failed: %w (stderr: %s)", err, stderr.String())

			// Check for secondary rate limit
			if shouldRetry, waitTime := handleSecondaryRateLimit(stderr.String(), nil, attempt); shouldRetry {
				pterm.Warning.Printf("⚠ Secondary rate limit hit for %s. Waiting %v (attempt %d/%d)\n",
					endpoint, waitTime, attempt+1, maxRateLimitRetries)
				time.Sleep(waitTime)
				continue
			}

			// Not a rate limit error or max retries exceeded
			return lastErr
		}

		// Parse the response - gh api --paginate returns each page as a separate JSON on a line
		response := stdout.String()
		lines := strings.Split(strings.TrimSpace(response), "\n")

		// Count the actual number of pages (each non-empty line is a page/API call)
		pageCount := 0
		for _, line := range lines {
			if line == "" {
				continue
			}
			pageCount++
			if err := callback([]byte(line)); err != nil {
				return err
			}
		}

		// Increment by the actual number of pages fetched
		if pageCount > 0 {
			for i := 0; i < pageCount; i++ {
				state.Get().IncrementAPICalls()
			}
		} else {
			// Even if no results, the initial call counts
			state.Get().IncrementAPICalls()
		}

		// Success
		return nil
	}

	return lastErr
}

// parseRESTResponse parses the REST API response with headers
func parseRESTResponse(response, endpoint string, verbose bool) ([]PackageResponse, int, error) {
	headers, body := extractHeadersAndBody(response)

	// Print headers for debugging if verbose
	if verbose && len(headers) > 0 {
		pterm.Info.Printf("Headers for %s:\n", endpoint)
		for name, value := range headers {
			pterm.Info.Printf("  %s: %s\n", name, value)
		}
	}

	// Try to parse body as a JSON array first (single page)
	var arrayResults []PackageResponse
	if err := json.Unmarshal([]byte(body), &arrayResults); err == nil {
		return arrayResults, 1, nil
	}

	// Fallback: Split by newlines and parse each line (multi-page)
	lines := strings.Split(strings.TrimSpace(body), "\n")
	var results []PackageResponse
	pageCount := 0
	for i, line := range lines {
		if line == "" {
			continue
		}
		pageCount++
		var result PackageResponse
		if err := json.Unmarshal([]byte(line), &result); err != nil {
			return nil, 0, fmt.Errorf("failed to parse REST response line %d: %w\nline: %s", i+1, err, line)
		}
		results = append(results, result)
	}

	if pageCount == 0 {
		pageCount = 1 // At least one API call was made
	}
	return results, pageCount, nil
}

// executeRESTPaginatedCall executes a REST API call with retries
func executeRESTPaginatedCall(ctx context.Context, endpoint string, verbose bool, attempt int) ([]PackageResponse, int, error) {
	cmd := BuildGHCommand(ctx, "api", "--paginate", "--include", endpoint)
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := stderr.String()

		// Check for secondary rate limit
		if shouldRetry, waitTime := handleSecondaryRateLimit(stderrStr, nil, attempt); shouldRetry {
			pterm.Warning.Printf("⚠ Secondary rate limit hit for %s. Waiting %v (attempt %d/%d)\n",
				endpoint, waitTime, attempt+1, maxRetries)
			time.Sleep(waitTime)
			return nil, 0, fmt.Errorf("retry needed")
		}

		// Check for gateway/server errors
		if strings.Contains(stderrStr, "504") || strings.Contains(stderrStr, "502") ||
			strings.Contains(stderrStr, "503") || strings.Contains(stderrStr, "500") ||
			strings.Contains(stderrStr, "Server Error") {
			backoff := time.Duration(math.Pow(2, float64(attempt))) * retryBackoffBaseDuration
			if backoff > maxBackoffDuration {
				backoff = maxBackoffDuration
			}
			pterm.Warning.Printf("⚠ Gateway/Server error for %s (HTTP 5xx), retrying in %v (attempt %d/%d)\n",
				endpoint, backoff, attempt+1, maxRetries)

			select {
			case <-ctx.Done():
				pterm.Info.Println("Operation cancelled, stopping retries")
				return nil, 0, ctx.Err()
			case <-time.After(backoff):
				return nil, 0, fmt.Errorf("retry needed")
			}
		}

		return nil, 0, fmt.Errorf("REST API call failed for %s: %w\nstderr: %s", endpoint, err, stderrStr)
	}

	return parseRESTResponse(stdout.String(), endpoint, verbose)
}

func RunRESTPaginated(ctx context.Context, endpoint string, verbose bool) ([]PackageResponse, error) {
	// Update rate limit info at the start
	UpdateRateLimitInfo()

	// Check rate limit before making REST call
	if err := state.Get().CheckRateLimit(5); err != nil {
		return nil, fmt.Errorf("rate limit check failed: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		// Check for cancellation before retry
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
		}

		results, pageCount, err := executeRESTPaginatedCall(ctx, endpoint, verbose, attempt)
		if err == nil {
			// Success - increment API calls and return
			for i := 0; i < pageCount; i++ {
				state.Get().IncrementAPICalls()
			}
			UpdateRateLimitInfo()
			return results, nil
		}

		// Check if we should retry
		if err.Error() == "retry needed" {
			continue
		}

		// Context cancellation
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Non-retryable error
		lastErr = err
		break
	}

	return nil, fmt.Errorf("all retries failed: %w", lastErr)
}

// extractHeadersAndBody parses HTTP response from GitHub CLI's --include flag output.
//
// The GitHub CLI's --include flag outputs HTTP headers followed by a blank line,
// then the response body. This function splits them into separate components for
// retry logic and error handling.
//
// Format expected:
//
//	HTTP/1.1 200 OK
//	Header-Name: value
//	Another-Header: value
//	[blank line]
//	{"json": "body"}
//
// Returns:
//   - headers: Map of header names (lowercase) to values
//   - body: The response body as a string
func extractHeadersAndBody(response string) (map[string]string, string) {
	headers := make(map[string]string)

	// Split response into lines
	lines := strings.Split(response, "\n")

	// Find the blank line that separates headers from body
	bodyStart := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			bodyStart = i + 1
			break
		}
	}

	// If no blank line found, assume entire response is body
	if bodyStart == -1 {
		return headers, response
	}

	// Parse headers (lines before the blank line)
	for i := 0; i < bodyStart-1; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}

		// Parse header in format "Name: Value"
		if colonIndex := strings.Index(line, ":"); colonIndex > 0 {
			name := strings.TrimSpace(line[:colonIndex])
			value := strings.TrimSpace(line[colonIndex+1:])
			headers[name] = value
		}
	}

	// Extract body (lines after the blank line)
	body := strings.Join(lines[bodyStart:], "\n")

	return headers, body
}

// startPackageProgressBar cycles the progress bar title through remaining package types until done is closed.
// Intended to be run as a goroutine. WaitGroup is used for synchronization.
func startPackageProgressBar(progress *pterm.ProgressbarPrinter, done <-chan struct{}, completed <-chan string, wg *sync.WaitGroup) {
	defer wg.Done()

	// Track which package types have been completed
	completedTypes := make(map[string]bool)
	remainingTypes := make([]string, len(SupportedPackageTypes))
	copy(remainingTypes, SupportedPackageTypes)

	// Simple counter for cycling through remaining types
	counter := 0

	for {
		select {
		case <-done:
			progress.UpdateTitle("Fetching packages done")
			time.Sleep(DoneMessageDisplayTime)
			_, _ = progress.Stop() // Error is not critical for UI cleanup
			pterm.Info.Println("Fetching packages done")
			return
		case completedType := <-completed:
			// Mark this package type as completed
			completedTypes[completedType] = true

			// Update remaining types list
			remainingTypes = remainingTypes[:0]
			for _, pkgType := range SupportedPackageTypes {
				if !completedTypes[pkgType] {
					remainingTypes = append(remainingTypes, pkgType)
				}
			}
		default:
			// Show remaining package types in rotation
			if len(remainingTypes) > 0 {
				// Use a simple counter to cycle through remaining types
				index := counter % len(remainingTypes)
				pkgType := remainingTypes[index]
				progress.UpdateTitle(fmt.Sprintf("Fetching %s packages... (%d remaining)", pkgType, len(remainingTypes)))
				counter++
			} else {
				progress.UpdateTitle("Processing package data...")
			}
			time.Sleep(PackageTypeCycleInterval)
		}
	}
}

// fetchPackagesForType fetches all packages of a given type for an org using the REST API.
func fetchPackagesForType(ctx context.Context, org, pkgType string, verbose bool) ([]PackageResponse, error) {
	endpoint := fmt.Sprintf("/orgs/%s/packages?package_type=%s", org, pkgType)
	return RunRESTPaginated(ctx, endpoint, verbose)
}

// convertToPackageData converts PackageResponse to PackageData for output.
func convertToPackageData(org string, results []PackageResponse) []output.PackageData {
	var packageData []output.PackageData
	for _, pkg := range results {
		repoName := pkg.Repository.Name
		if repoName == "" {
			repoName = "unassigned" // Packages not associated with a specific repository
		}

		packageData = append(packageData, output.PackageData{
			Org:          org,
			Repo:         repoName,
			PackageName:  pkg.Name,
			PackageType:  pkg.Type,
			Visibility:   pkg.Visibility,
			CreatedAt:    pkg.CreatedAt,
			UpdatedAt:    pkg.UpdatedAt,
			VersionCount: pkg.VersionCount,
		})
	}
	return packageData
}

// GetOrgPackageDataWithResume fetches package data for each repository in an org,
// with support for resuming from existing package data. Returns both package counts and version counts.
// If existingCounts is provided and not empty, it will be used instead of fetching new data.
// useExistingPackageData returns existing package data if available
func useExistingPackageData(org string, progress *pterm.ProgressbarPrinter, existingCounts map[string]output.PackageCounts) (map[string]output.PackageCounts, []output.PackageData, bool) {
	if len(existingCounts) == 0 {
		return nil, nil, false
	}

	if progress != nil {
		progress.UpdateTitle("Using existing package data")
		for i := 0; i < len(SupportedPackageTypes); i++ {
			progress.Increment()
		}
		_, _ = progress.Stop() // Error is not critical for UI cleanup
	}
	pterm.Info.Printf("Using existing package data for %s (%d repositories)\n", org, len(existingCounts))
	return existingCounts, []output.PackageData{}, true
}

// fetchPackagesWorker fetches packages for a single type and aggregates results
func fetchPackagesWorker(ctx context.Context, org, pkgType string, verbose bool, progress *pterm.ProgressbarPrinter,
	repoPackages map[string]map[string]output.PackageData, countsMu *sync.Mutex,
	allPackageData *[]output.PackageData, packageDataMu *sync.Mutex,
	completed chan<- string) {

	results, err := fetchPackagesForType(ctx, org, pkgType, verbose)
	if err == nil {
		countsMu.Lock()
		// Aggregate packages with full data
		for _, pkg := range results {
			repoName := pkg.Repository.Name
			if repoName == "" {
				repoName = "unassigned" // Packages not associated with a specific repository
			}

			if _, ok := repoPackages[repoName]; !ok {
				repoPackages[repoName] = make(map[string]output.PackageData)
			}
			repoPackages[repoName][pkg.Name] = output.PackageData{
				Org:          org,
				Repo:         repoName,
				PackageName:  pkg.Name,
				PackageType:  pkg.Type,
				Visibility:   pkg.Visibility,
				CreatedAt:    pkg.CreatedAt,
				UpdatedAt:    pkg.UpdatedAt,
				VersionCount: pkg.VersionCount,
			}
		}
		countsMu.Unlock()

		// Collect all package data
		packageDataMu.Lock()
		*allPackageData = append(*allPackageData, convertToPackageData(org, results)...)
		packageDataMu.Unlock()

		if progress != nil {
			progress.Increment()
		}

		// Notify that this package type is completed
		select {
		case completed <- pkgType:
		default:
		}
	} else {
		// Log warning but don't fail - package errors are non-critical
		if verbose {
			pterm.Warning.Printf("Failed to fetch %s packages for %s (continuing): %v\n", pkgType, org, err)
		}
		// Mark as completed even if failed, so progress bar updates
		if progress != nil {
			progress.Increment()
		}
		select {
		case completed <- pkgType:
		default:
		}
	}
}

// convertRepoPackagesToCounts converts aggregated package data to PackageCounts
func convertRepoPackagesToCounts(org string, repoPackages map[string]map[string]output.PackageData) map[string]output.PackageCounts {
	counts := make(map[string]output.PackageCounts)
	for repo, pkgs := range repoPackages {
		packageCount := len(pkgs)
		versionCount := 0

		// Sum up version counts for all packages in this repository
		for _, pkg := range pkgs {
			if pkg.VersionCount != nil {
				versionCount += *pkg.VersionCount
			}
		}

		// Use "org/repo" format as the key to match what's used in repository.go
		repoKey := fmt.Sprintf("%s/%s", org, repo)
		counts[repoKey] = output.PackageCounts{
			PackageCount:    packageCount,
			PackageVersions: versionCount,
		}
	}
	return counts
}

func GetOrgPackageDataWithResume(ctx context.Context, org string, progress *pterm.ProgressbarPrinter, existingCounts map[string]output.PackageCounts, verbose bool) (map[string]output.PackageCounts, []output.PackageData, error) {
	// If we have existing counts and they're not empty, use them
	if counts, data, ok := useExistingPackageData(org, progress, existingCounts); ok {
		return counts, data, nil
	}

	repoPackages := make(map[string]map[string]output.PackageData)
	var countsMu sync.Mutex
	var allPackageData []output.PackageData
	var packageDataMu sync.Mutex

	done := make(chan struct{})
	completed := make(chan string, len(SupportedPackageTypes))
	var cyclingWG sync.WaitGroup
	if progress != nil {
		cyclingWG.Add(1)
		go startPackageProgressBar(progress, done, completed, &cyclingWG)
	}

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, MaxConcurrentPackageFetches)

	for _, pkgType := range SupportedPackageTypes {
		wg.Add(1)
		go func(pkgType string) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			fetchPackagesWorker(ctx, org, pkgType, verbose, progress, repoPackages, &countsMu, &allPackageData, &packageDataMu, completed)
		}(pkgType)
	}

	wg.Wait()
	close(done)
	cyclingWG.Wait()

	counts := convertRepoPackagesToCounts(org, repoPackages)
	return counts, allPackageData, nil
}

// GetRateLimit fetches the current GitHub API rate limit information
func GetRateLimit() (*RateLimitResponse, error) {
	cmd := BuildGHCommand(context.Background(), "api", "rate_limit")
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to fetch rate limit: %w\nstderr: %s", err, stderr.String())
	}

	var response RateLimitResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return nil, fmt.Errorf("failed to parse rate limit response: %w", err)
	}

	return &response, nil
}

// UpdateRateLimitInfo fetches and updates the current rate limit information
func UpdateRateLimitInfo() {
	rateLimit, err := GetRateLimit()
	if err != nil {
		// Don't fail the entire operation if we can't get rate limit info
		return
	}

	// Convert Unix timestamp to time.Time for REST API
	resetTime := time.Unix(rateLimit.Resources.Core.Reset, 0)

	state.Get().UpdateRateLimit(
		rateLimit.Resources.Core.Limit,
		rateLimit.Resources.Core.Remaining,
		resetTime,
	)

	// Update GraphQL rate limit as well
	graphqlResetTime := time.Unix(rateLimit.Resources.GraphQL.Reset, 0)
	state.Get().UpdateGraphQLRateLimit(
		rateLimit.Resources.GraphQL.Limit,
		rateLimit.Resources.GraphQL.Used,
		rateLimit.Resources.GraphQL.Remaining,
		graphqlResetTime,
	)
}
