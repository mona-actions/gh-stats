// Package ghapi provides GitHub API client functionality.
//
// This file (core_pagination.go) implements REST API pagination utilities for GitHub.
// It handles Link header parsing, page counting, and automatic pagination for large
// result sets from GitHub's REST API endpoints.
package ghapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mona-actions/gh-stats/internal/state"
	"github.com/pterm/pterm"
)

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
	// Look for the rel we want (e.g., "last", "next")
	relPattern := fmt.Sprintf(`rel="%s"`, rel)
	for _, link := range strings.Split(linkHeader, ",") {
		if !strings.Contains(link, relPattern) {
			continue
		}

		// Extract URL between < and >
		if urlPart, _, ok := strings.Cut(link, ">"); ok {
			if url, ok := strings.CutPrefix(urlPart, "<"); ok {
				// Extract query string and parse page parameter
				if _, query, ok := strings.Cut(url, "?"); ok {
					for _, param := range strings.Split(query, "&") {
						if key, val, ok := strings.Cut(param, "="); ok && key == "page" {
							var pageNum int
							if _, err := fmt.Sscanf(val, "%d", &pageNum); err == nil {
								return pageNum, true
							}
						}
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
// Parameters:
//   - ctx: Context for cancellation
//   - endpoint: GitHub API endpoint to query
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

		cmd := exec.CommandContext(ctx, "gh", "api", "--include", endpoint)
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

// countAndTrackPages counts the number of pages in a paginated response and tracks API usage.
//
// The `gh api --paginate` command returns multiple JSON objects separated by newlines,
// one per page. This function counts them and increments the API call counter accordingly.
//
// Parameters:
//   - output: The full paginated response string
//
// Note: Each page counts as a separate API call, so this function calls
// state.Get().IncrementAPICalls() for each page found.
func countAndTrackPages(output string) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	pageCount := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			pageCount++
		}
	}

	if pageCount > 0 {
		for i := 0; i < pageCount; i++ {
			state.Get().IncrementAPICalls()
		}
	} else {
		state.Get().IncrementAPICalls()
	}
}
