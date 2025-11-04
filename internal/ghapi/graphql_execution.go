// Package ghapi provides GitHub API client functionality.
//
// This file (graphql.go) contains GraphQL query operations for bulk data fetching.
// It provides efficient functions to fetch repository lists and comprehensive repository
// data using GitHub's GraphQL API.
//
// Key features:
//   - GraphQL-based repository enumeration
//   - Comprehensive repository metadata fetching
//   - Pagination handling for large organizations
//   - Team access pre-fetching optimization
//   - Efficient bulk data collection
//   - Per-operation timeouts (5 minutes) to prevent hangs
//   - Batching optimization: cheap fields batched, expensive fields fetched per-repo
package ghapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/mona-actions/gh-stats/internal/state"
	"github.com/pterm/pterm"
)

// buildGraphQLArgs constructs the command-line arguments for a GraphQL API call.
func buildGraphQLArgs(query string, variables map[string]interface{}, endCursor string) []string {
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

	return args
}

// executeGraphQLCommand runs the gh CLI command and returns the result or an error.
func executeGraphQLCommand(ctx context.Context, args []string, pageCount int) ([]byte, string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := stderr.String()
		return nil, stderrStr, fmt.Errorf("GraphQL API call failed (page %d): %w\nstderr: %s", pageCount, err, stderrStr)
	}

	return stdout.Bytes(), "", nil
}

// parseGraphQLResponse parses and validates the GraphQL response.
func parseGraphQLResponse(responseBytes []byte, pageCount int) (map[string]interface{}, error) {
	var result map[string]interface{}
	if err := json.Unmarshal(responseBytes, &result); err != nil {
		return nil, fmt.Errorf("failed to parse GraphQL response (page %d): %w\nresponse: %s", pageCount, err, string(responseBytes))
	}

	// Check for GraphQL errors
	if errors, ok := result["errors"].([]interface{}); ok && len(errors) > 0 {
		return nil, fmt.Errorf("GraphQL errors on page %d: %v", pageCount, errors)
	}

	return result, nil
}

// executeGraphQLPage executes a single GraphQL page with retries.
func executeGraphQLPage(ctx context.Context, query string, variables map[string]interface{}, endCursor string, pageCount int) (map[string]interface{}, error) {
	var lastErr error

	// Outer retry loop for transient server errors (5xx)
	for serverRetry := 0; serverRetry < maxRetries; serverRetry++ {
		// Inner retry loop for secondary rate limits
		for rateLimitRetry := 0; rateLimitRetry < maxRateLimitRetries; rateLimitRetry++ {
			// Check for context cancellation before retry
			if serverRetry > 0 || rateLimitRetry > 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
				}
			}

			// Build command arguments
			args := buildGraphQLArgs(query, variables, endCursor)

			// Execute the command
			responseBytes, stderrStr, err := executeGraphQLCommand(ctx, args, pageCount)
			if err != nil {
				lastErr = err

				// Check for secondary rate limit
				if shouldRetry, waitTime := handleSecondaryRateLimit(stderrStr, nil, rateLimitRetry); shouldRetry {
					pterm.Warning.Printf("⚠ Secondary rate limit hit on GraphQL page %d. Waiting %v (rate limit attempt %d/%d)\n",
						pageCount, waitTime, rateLimitRetry+1, maxRateLimitRetries)
					time.Sleep(waitTime)
					continue // Continue inner rate limit retry loop
				}

				// Check for transient errors (5xx, network issues, timeouts)
				if handleTransientErrorRetry(ctx, stderrStr, serverRetry) {
					break // Break inner loop to retry in outer loop
				}

				// Not a retryable error
				if !isTransientError(stderrStr) {
					return nil, lastErr
				}

				// Last retry attempt for transient error
				break
			}

			// Success! Process the response
			state.Get().IncrementAPICalls()

			// Update rate limit info every 50 API calls
			if pageCount%50 == 0 {
				UpdateRateLimitInfo()
				state.Get().PrintRateLimit()
			}

			// Parse and validate the response
			result, err := parseGraphQLResponse(responseBytes, pageCount)
			if err != nil {
				return nil, err
			}

			return result, nil
		}

		// Check if we should continue outer loop for transient errors
		if lastErr != nil && !isTransientError(lastErr.Error()) {
			break
		}
	}

	return nil, lastErr
}

// RunGraphQLPaginated executes a GraphQL query with pagination support.
// It repeatedly calls the query until all pages are fetched, using the pageFunc
// callback to extract pagination information from each response.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - query: GraphQL query string with pagination support
//   - variables: Query variables (may be modified by pageFunc for pagination)
//   - pageFunc: Callback that returns (endCursor, hasNextPage) for pagination
//
// Returns all pages as a slice of response maps, or an error if any page fails.
// Automatically handles rate limiting, retries, and API call tracking.
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
			// Return partial results with error instead of losing all progress
			if len(allPages) > 0 {
				pterm.Warning.Printf("⚠ Failed on page %d, returning %d pages already collected\n", pageCount, len(allPages))
				return allPages, fmt.Errorf("pagination incomplete (collected %d pages, failed on page %d): %w", len(allPages), pageCount, err)
			}
			// No pages collected yet, return the error
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

// getBaseOnlyConfig returns a config with only cheap/base fields enabled.
// This is used for batching to keep queries small and fast.
// Expensive fields are fetched separately per-repo.
