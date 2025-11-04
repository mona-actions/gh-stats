// Package ghapi provides GitHub API client functionality.
//
// This file (core_rest.go) implements the core REST API client for GitHub.
// It provides functions for executing REST API calls with automatic retry logic,
// error handling, and response processing.
package ghapi

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mona-actions/gh-stats/internal/state"
	"github.com/pterm/pterm"
)

// executeRESTCall executes a REST API call with retry logic for transient errors.
// This is a centralized helper to ensure ALL REST API calls have consistent retry behavior.
//
// Parameters:
//   - ctx: Context for cancellation
//   - endpoint: GitHub API endpoint (e.g., "/repos/org/repo/branches")
//   - verbose: Whether to log retry attempts
//
// Returns:
//   - stdout: Command output as string
//   - stderr: Command error output as string
//   - error: Non-nil if all retries exhausted
func executeRESTCall(ctx context.Context, endpoint string, verbose bool) (string, string, error) {
	const maxRetries = 3
	const baseRetryDelay = 2 * time.Second

	var lastErr error
	var lastStderr string

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Check for cancellation before retry (except first attempt)
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", "", ctx.Err()
			default:
			}
		}

		cmd := exec.CommandContext(ctx, "gh", "api", endpoint)
		cmd.Env = os.Environ()

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			stderrStr := stderr.String()
			lastStderr = stderrStr
			lastErr = fmt.Errorf("failed to execute API request: %w (stderr: %s)", err, stderrStr)

			if isTransientRESTError(stderrStr) && attempt < maxRetries-1 {
				retryDelay := baseRetryDelay * time.Duration(1<<uint(attempt)) // Exponential backoff: 2s, 4s, 8s
				if verbose {
					pterm.Warning.Printf("⚠️  %s (attempt %d/%d, retrying in %v)\n",
						endpoint, attempt+1, maxRetries, retryDelay)
				}
				time.Sleep(retryDelay)
				continue
			}

			// Not transient or last attempt - return error
			return "", stderrStr, lastErr
		}

		// Success!
		return stdout.String(), "", nil
	}

	return "", lastStderr, lastErr
}

// RunRESTCallback executes a REST API call with pagination and calls the callback for each page.

// processRESTResponse processes the REST API response and calls the callback for each page.
// The gh api --paginate command returns each page as a separate JSON line.
//
// Parameters:
//   - response: The full paginated response string
//   - callback: Function to call for each page
//
// Returns:
//   - pageCount: Number of pages processed
//   - error: Non-nil if callback returns error
func processRESTResponse(response string, callback func([]byte) error) (int, error) {
	// gh api --paginate returns each page as a separate JSON on a line
	lines := strings.Split(strings.TrimSpace(response), "\n")

	// Count the actual number of pages (each non-empty line is a page/API call)
	pageCount := 0
	for _, line := range lines {
		if line == "" {
			continue
		}
		pageCount++
		if err := callback([]byte(line)); err != nil {
			return pageCount, err
		}
	}

	return pageCount, nil
}

// RunRESTCallback executes a REST API call with pagination and calls the callback for each page.
// This is a generic helper for REST pagination that processes data through a callback.
//
// Parameters:
//   - ctx: Context for cancellation
//   - endpoint: GitHub API endpoint
//   - callback: Function to call for each page of results
//
// Returns:
//   - error: Non-nil if API call fails or callback returns error
func RunRESTCallback(ctx context.Context, endpoint string, callback func([]byte) error) error {
	if err := state.Get().CheckRateLimit(5); err != nil {
		return fmt.Errorf("rate limit check failed: %w", err)
	}

	var lastErr error

	// Outer retry loop for transient errors (timeouts, network issues)
	for serverRetry := 0; serverRetry < maxRetries; serverRetry++ {
		// Inner retry loop for secondary rate limits
		for attempt := 0; attempt < maxRateLimitRetries; attempt++ {
			// Check for cancellation before retry (but not on first attempt)
			if serverRetry > 0 || attempt > 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
			}

			cmd := exec.CommandContext(ctx, "gh", "api", "--paginate", endpoint)
			cmd.Env = os.Environ()

			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			if err := cmd.Run(); err != nil {
				stderrStr := stderr.String()
				lastErr = fmt.Errorf("REST API call failed: %w (stderr: %s)", err, stderrStr)

				// Check for secondary rate limit
				if shouldRetry, waitTime := handleSecondaryRateLimit(stderrStr, nil, attempt); shouldRetry {
					pterm.Warning.Printf("⚠ Secondary rate limit hit for %s. Waiting %v (attempt %d/%d)\n",
						endpoint, waitTime, attempt+1, maxRateLimitRetries)
					time.Sleep(waitTime)
					continue // Continue inner rate limit retry loop
				}

				// Check for transient network errors
				if handleRESTNetworkRetry(ctx, stderrStr, serverRetry, endpoint) {
					break // Break inner loop to retry in outer loop
				}

				// Not a retryable error or max retries exceeded
				return lastErr
			}

			// Success - process the response
			pageCount, err := processRESTResponse(stdout.String(), callback)
			if err != nil {
				return err
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
		} // End inner retry loop
	} // End outer retry loop

	return lastErr
}

// executeRESTPaginatedSimple executes a paginated REST API call with retries and returns raw JSON output.
// This is for simple cases where you just want the aggregated JSON response from --paginate.
//
// Parameters:
//   - ctx: Context for cancellation
//   - endpoint: GitHub API endpoint
//   - verbose: Whether to log retry attempts
//
// Returns:
//   - output: The full paginated response as string
//   - error: Non-nil if all retries exhausted
func executeRESTPaginatedSimple(ctx context.Context, endpoint string, verbose bool) (string, error) {
	if err := state.Get().CheckRateLimit(5); err != nil {
		return "", fmt.Errorf("rate limit check failed: %w", err)
	}

	var lastErr error

	// Outer retry loop for transient errors
	for serverRetry := 0; serverRetry < maxRetries; serverRetry++ {
		// Inner retry loop for secondary rate limits
		for rateLimitRetry := 0; rateLimitRetry < maxRateLimitRetries; rateLimitRetry++ {
			// Check for cancellation before retry
			if serverRetry > 0 || rateLimitRetry > 0 {
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				default:
				}
			}

			cmd := exec.CommandContext(ctx, "gh", "api", "--paginate", endpoint)
			cmd.Env = os.Environ()

			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			if err := cmd.Run(); err != nil {
				stderrStr := stderr.String()
				shouldContinue, shouldBreak, finalErr := handlePaginatedRESTError(
					ctx, stderrStr, endpoint, serverRetry, rateLimitRetry, verbose)

				if finalErr != nil {
					lastErr = finalErr
				}
				if shouldContinue {
					continue
				}
				if shouldBreak {
					break
				}
			} else {
				// Success!
				output := stdout.String()
				countAndTrackPages(output)
				return output, nil
			}
		}
	}

	return "", lastErr
}
