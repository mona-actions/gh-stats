// Package ghapi provides GitHub API client functionality.
//
// This file (core_retry.go) implements retry logic with exponential backoff for GitHub API calls.
// It handles transient errors, rate limiting, and server errors with configurable retry attempts
// and intelligent backoff strategies.
package ghapi

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/pterm/pterm"
)

// Retry configuration constants
const (
	maxRetries               = 3
	maxRateLimitRetries      = 3
	retryBackoffBaseDuration = 1 * time.Second
	maxBackoffDuration       = 30 * time.Second
	minRetryWait             = 60 * time.Second
	maxRetryWait             = 15 * time.Minute
)

// parseRetryAfter parses the Retry-After HTTP header value.
//
// The header can be either:
//   - An integer representing seconds to wait.
//   - An HTTP date (RFC1123 format) representing absolute retry time.
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

// detectSecondaryRateLimit checks if the error indicates a secondary rate limit.
// Returns the wait duration and true if a rate limit is detected.
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

// isTransientError checks if an error is transient (5xx errors, network issues, timeouts).
// These errors should be retried with backoff.
func isTransientError(errStr string) bool {
	return strings.Contains(errStr, "500") ||
		strings.Contains(errStr, "502") ||
		strings.Contains(errStr, "503") ||
		strings.Contains(errStr, "504") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "connection reset")
}

// handleTransientErrorRetry handles retry logic for transient errors with exponential backoff.
// Returns true if the caller should break the inner loop to retry in the outer loop.
func handleTransientErrorRetry(ctx context.Context, stderrStr string, serverRetry int) bool {
	if !isTransientError(stderrStr) {
		return false
	}

	if serverRetry >= maxRetries-1 {
		pterm.Warning.Println("⚠ Max retries reached for transient error")
		return false
	}

	// Exponential backoff
	backoff := time.Duration(math.Pow(2, float64(serverRetry))) * retryBackoffBaseDuration
	if backoff > maxBackoffDuration {
		backoff = maxBackoffDuration
	}

	pterm.Warning.Printf("⚠ Transient error detected, retrying in %v (attempt %d/%d)\n",
		backoff, serverRetry+1, maxRetries)

	// Sleep with context cancellation support
	select {
	case <-ctx.Done():
		return false
	case <-time.After(backoff):
		return true
	}
}

// isTransientRESTError checks if a REST API error should be retried.
// This includes network errors, timeouts, and 5xx server errors.
func isTransientRESTError(stderrStr string) bool {
	return strings.Contains(stderrStr, "TLS handshake timeout") ||
		strings.Contains(stderrStr, "timeout") ||
		strings.Contains(stderrStr, "connection refused") ||
		strings.Contains(stderrStr, "connection reset") ||
		strings.Contains(stderrStr, "502 Bad Gateway") ||
		strings.Contains(stderrStr, "503 Service Unavailable") ||
		strings.Contains(stderrStr, "504 Gateway Timeout") ||
		strings.Contains(stderrStr, "EOF") ||
		strings.Contains(stderrStr, "i/o timeout")
}

// isRESTNetworkError checks if an error is a network/timeout error for REST API calls.
// Similar to isTransientRESTError but more focused on network-specific issues.
func isRESTNetworkError(stderrStr string) bool {
	return strings.Contains(stderrStr, "timeout") ||
		strings.Contains(stderrStr, "i/o timeout") ||
		strings.Contains(stderrStr, "connection reset") ||
		strings.Contains(stderrStr, "dial tcp") ||
		strings.Contains(stderrStr, "TLS handshake")
}

// handleRESTNetworkRetry handles retry logic for REST network errors with exponential backoff.
// Returns true if the caller should break the inner loop to retry in the outer loop.
func handleRESTNetworkRetry(ctx context.Context, stderrStr string, serverRetry int, endpoint string) bool {
	if !isRESTNetworkError(stderrStr) {
		return false
	}

	if serverRetry >= maxRetries-1 {
		return false // Last attempt, don't retry
	}

	backoff := time.Duration(math.Pow(2, float64(serverRetry))) * retryBackoffBaseDuration
	if backoff > maxBackoffDuration {
		backoff = maxBackoffDuration
	}

	pterm.Warning.Printf("⚠ Network error for REST API %s, retrying in %v (attempt %d/%d)\n",
		endpoint, backoff, serverRetry+1, maxRetries)

	select {
	case <-ctx.Done():
		return false
	case <-time.After(backoff):
		return true
	}
}

// handlePaginatedRESTError handles errors for paginated REST calls with retry logic.
// Returns: shouldContinue (continue inner loop), shouldBreak (break to outer loop), finalError
func handlePaginatedRESTError(ctx context.Context, stderrStr, endpoint string, serverRetry, rateLimitRetry int, verbose bool) (bool, bool, error) {
	lastErr := fmt.Errorf("paginated REST API call failed: stderr: %s", stderrStr)

	// Check for secondary rate limit
	if shouldRetry, waitTime := handleSecondaryRateLimit(stderrStr, nil, rateLimitRetry); shouldRetry {
		if verbose {
			pterm.Warning.Printf("⚠ Secondary rate limit hit for %s. Waiting %v (attempt %d/%d)\n",
				endpoint, waitTime, rateLimitRetry+1, maxRateLimitRetries)
		}
		time.Sleep(waitTime)
		return true, false, nil // Continue inner loop
	}

	// Check for transient network errors
	isTransient := isRESTNetworkError(stderrStr) || isTransientError(stderrStr)
	if isTransient && serverRetry < maxRetries-1 {
		backoff := time.Duration(math.Pow(2, float64(serverRetry))) * retryBackoffBaseDuration
		if backoff > maxBackoffDuration {
			backoff = maxBackoffDuration
		}
		if verbose {
			pterm.Warning.Printf("⚠ Network error for %s, retrying in %v (attempt %d/%d)\n",
				endpoint, backoff, serverRetry+1, maxRetries)
		}
		time.Sleep(backoff)
		return false, true, nil // Break inner loop to retry in outer loop
	}

	// Not retryable or last attempt
	if rateLimitRetry >= maxRateLimitRetries-1 && serverRetry >= maxRetries-1 {
		return false, false, lastErr
	}

	return false, false, nil
}
