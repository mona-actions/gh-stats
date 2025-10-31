// Package state provides global state tracking for statistics collection progress.
//
// This package manages global state for the gh-stats tool, including progress tracking,
// rate limit information, and API call counting. All state operations are thread-safe
// and suitable for concurrent use across multiple goroutines.
//
// Key features:
//   - Thread-safe progress tracking for repositories and organizations
//   - REST and GraphQL API rate limit monitoring
//   - Automatic rate limit enforcement with configurable safety buffer
//   - Graceful rate limit exhaustion handling
//   - API call count tracking for usage reporting
package state

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pterm/pterm"
)

// Sentinel errors for rate limit handling
var (
	// ErrRateLimitRefreshNeeded indicates rate limit data should be refreshed before retrying
	ErrRateLimitRefreshNeeded = errors.New("rate limit refresh needed")
	// ErrClockSkewDetected indicates a clock skew between client and GitHub servers
	ErrClockSkewDetected = errors.New("clock skew detected - rate limit reset time is in the past")
)

// Rate limit configuration constants.
const (
	rateLimitSafetyBuffer int64 = 200             // Conservative buffer to avoid hitting 429 errors with concurrent workers
	maxSleepUntilReset          = 2 * time.Hour   // Maximum time to wait for rate limit reset
	resetBufferTime             = 5 * time.Second // Buffer after reset to ensure limit has actually reset
)

// RateLimitInfo holds GitHub REST API rate limit information.
//
// Thread-safety: This type itself is not thread-safe. It is protected by
// Status.rateLimitMu when accessed through Status methods. Do not access
// directly from multiple goroutines without external synchronization.
//
// Zero value: A zero Limit indicates uninitialized or unavailable rate limit data.
type RateLimitInfo struct {
	Limit     int64     // Maximum requests allowed per hour
	Remaining int64     // Requests remaining in current window
	Reset     time.Time // When the rate limit window resets
}

// GraphQLRateLimitInfo holds GitHub GraphQL API rate limit information.
//
// Thread-safety: This type itself is not thread-safe. It is protected by
// Status.rateLimitMu when accessed through Status methods. Do not access
// directly from multiple goroutines without external synchronization.
//
// Zero value: A zero Limit indicates uninitialized or unavailable rate limit data.
type GraphQLRateLimitInfo struct {
	Limit     int64     // Maximum requests allowed per hour
	Used      int64     // Requests consumed in current window (increases as calls are made)
	Remaining int64     // Requests remaining in current window
	Reset     time.Time // When the rate limit window resets
}

// Status tracks the progress and API call counts for the current run.
//
// Thread-safety: All methods are safe for concurrent use from multiple goroutines.
// Internal synchronization uses atomic operations for counters and RWMutex for
// complex data structures.
//
// Synchronization strategy:
// - Simple counters (repoTotal, repoDone, apiCalls) use atomic operations for lock-free access
// - Rate limit info uses RWMutex because it's a complex struct that needs consistent reads
// - This mixed approach optimizes for the common case: frequent counter updates, infrequent rate limit updates
type Status struct {
	repoTotal        int64
	repoDone         int64
	apiCalls         int64
	startingAPICalls int64 // Track starting API calls from rate limit

	rateLimitMu         sync.RWMutex
	rateLimit           RateLimitInfo
	graphqlRateLimit    GraphQLRateLimitInfo
	startingGraphQLUsed int64 // Track starting GraphQL usage
}

var global = &Status{}

// Get returns the global Status instance for tracking progress and API calls.
func Get() *Status {
	return global
}

// PrintRepo prints the status of a processed repository (success or warning) and increments the done count.
func (s *Status) PrintRepo(repoName string, success bool, errMsg string) {
	if success {
		pterm.Success.Printf("✓ %s\n", repoName)
	} else {
		if errMsg != "" {
			pterm.Warning.Printf("⚠ %s: %s\n", repoName, errMsg)
		} else {
			pterm.Warning.Printf("⚠ %s\n", repoName)
		}
	}
	atomic.AddInt64(&s.repoDone, 1)
}

// PrintOrg prints the organization being processed.
func (s *Status) PrintOrg(orgName string) {
	pterm.Info.Printf("Processing organization: %s\n", orgName)
}

// MarkDone prints a final summary of the run, including total repos and API calls.
func (s *Status) MarkDone() {
	repoDone := atomic.LoadInt64(&s.repoDone)
	repoTotal := atomic.LoadInt64(&s.repoTotal)

	// Calculate actual REST API calls used from rate limit difference
	rateLimit := s.GetRateLimit()
	startingRemaining := atomic.LoadInt64(&s.startingAPICalls)

	var actualRESTCalls int64
	if rateLimit.Limit > 0 && startingRemaining > 0 {
		// REST API tracks by "remaining" count: it goes DOWN as we use calls
		// So: calls used = starting remaining - current remaining
		actualRESTCalls = startingRemaining - rateLimit.Remaining
	}

	// Calculate GraphQL API usage from used counter difference
	// GraphQL tracks by "used" count: it goes UP as we use calls
	graphqlRateLimit := s.GetGraphQLRateLimit()
	startingGraphQLUsed := atomic.LoadInt64(&s.startingGraphQLUsed)
	actualGraphQLCalls := graphqlRateLimit.Used - startingGraphQLUsed

	status := fmt.Sprintf("Complete! Processed %d/%d repos | REST API: %d calls | GraphQL API: %d calls",
		repoDone, repoTotal, actualRESTCalls, actualGraphQLCalls)
	pterm.Success.Printf("✓ %s\n", status)
}

// CaptureStartingAPICalls captures the starting API calls from rate limit for accurate tracking
func (s *Status) CaptureStartingAPICalls() {
	rateLimit := s.GetRateLimit()
	graphqlLimit := s.GetGraphQLRateLimit()
	if rateLimit.Limit > 0 {
		atomic.StoreInt64(&s.startingAPICalls, rateLimit.Remaining)
	}
	if graphqlLimit.Limit > 0 {
		atomic.StoreInt64(&s.startingGraphQLUsed, graphqlLimit.Used)
	}
}

// AddRepos increments the total repository count (thread-safe).
func (s *Status) AddRepos(n int) {
	atomic.AddInt64(&s.repoTotal, int64(n))
}

// IncrementAPICalls increments the API call count (thread-safe).
func (s *Status) IncrementAPICalls() {
	atomic.AddInt64(&s.apiCalls, 1)
}

// GetAPICalls returns the current API call count (thread-safe).
func (s *Status) GetAPICalls() int64 {
	return atomic.LoadInt64(&s.apiCalls)
}

// UpdateRateLimit updates the rate limit information (thread-safe).
func (s *Status) UpdateRateLimit(limit, remaining int64, reset time.Time) {
	s.rateLimitMu.Lock()
	defer s.rateLimitMu.Unlock()
	s.rateLimit = RateLimitInfo{
		Limit:     limit,
		Remaining: remaining,
		Reset:     reset,
	}
}

// UpdateGraphQLRateLimit updates the GraphQL rate limit information (thread-safe).
func (s *Status) UpdateGraphQLRateLimit(limit, used, remaining int64, reset time.Time) {
	s.rateLimitMu.Lock()
	defer s.rateLimitMu.Unlock()
	s.graphqlRateLimit = GraphQLRateLimitInfo{
		Limit:     limit,
		Used:      used,
		Remaining: remaining,
		Reset:     reset,
	}
}

// GetRateLimit returns the current rate limit information (thread-safe).
func (s *Status) GetRateLimit() RateLimitInfo {
	s.rateLimitMu.RLock()
	defer s.rateLimitMu.RUnlock()
	return s.rateLimit
}

// GetGraphQLRateLimit returns the current GraphQL rate limit information (thread-safe).
func (s *Status) GetGraphQLRateLimit() GraphQLRateLimitInfo {
	s.rateLimitMu.RLock()
	defer s.rateLimitMu.RUnlock()
	return s.graphqlRateLimit
}

// PrintRateLimit prints the current rate limit status.
func (s *Status) PrintRateLimit() {
	rateLimit := s.GetRateLimit()
	graphqlLimit := s.GetGraphQLRateLimit()

	if rateLimit.Limit > 0 && graphqlLimit.Limit > 0 {
		restUsed := rateLimit.Limit - rateLimit.Remaining

		// Format reset times, handling zero values
		restReset := "unknown"
		if !rateLimit.Reset.IsZero() {
			restReset = rateLimit.Reset.Format("15:04:05")
		}

		graphqlReset := "unknown"
		if !graphqlLimit.Reset.IsZero() {
			graphqlReset = graphqlLimit.Reset.Format("15:04:05")
		}

		pterm.Info.Printf("REST: %d/%d used (%d remaining, resets at %s) | GraphQL: %d/%d used (%d remaining, resets at %s)\n",
			restUsed, rateLimit.Limit, rateLimit.Remaining, restReset,
			graphqlLimit.Used, graphqlLimit.Limit, graphqlLimit.Remaining, graphqlReset)
	} else if rateLimit.Limit > 0 {
		restUsed := rateLimit.Limit - rateLimit.Remaining

		restReset := "unknown"
		if !rateLimit.Reset.IsZero() {
			restReset = rateLimit.Reset.Format("15:04:05")
		}

		pterm.Info.Printf("%d/%d calls used | %d remaining | resets at: %s\n",
			restUsed, rateLimit.Limit, rateLimit.Remaining, restReset)
	}
}

// CheckRateLimit checks if we're approaching rate limits and sleeps if necessary.
// Returns an error if we can't proceed (e.g., rate limit hit and reset time is too far away).
// Uses a safety buffer to avoid hitting 429 errors.
//
// This function checks BOTH REST and GraphQL API rate limits and uses the most restrictive one.
// It handles clock skew, concurrent workers, and provides clear user feedback.
func (s *Status) CheckRateLimit(minRequired int64) error {
	rateLimit := s.GetRateLimit()
	graphqlLimit := s.GetGraphQLRateLimit()

	// If we don't have rate limit info yet, skip check
	if rateLimit.Limit == 0 {
		return nil
	}

	// Check both REST and GraphQL limits
	// REST API uses "remaining" (decreases as we use it)
	// GraphQL API uses "used" + "remaining" (remaining decreases as we use it)
	restAvailable := rateLimit.Remaining - rateLimitSafetyBuffer
	graphqlAvailable := int64(0)

	if graphqlLimit.Limit > 0 {
		graphqlAvailable = graphqlLimit.Remaining - rateLimitSafetyBuffer
	}

	// Determine which limit is more restrictive
	var limitType string
	var available int64
	var resetTime time.Time
	var remaining int64

	// Use the more restrictive limit (lower available calls)
	if graphqlLimit.Limit > 0 && (restAvailable > graphqlAvailable || rateLimit.Limit == 0) {
		limitType = "GraphQL"
		available = graphqlAvailable
		resetTime = graphqlLimit.Reset
		remaining = graphqlLimit.Remaining
	} else {
		limitType = "REST"
		available = restAvailable
		resetTime = rateLimit.Reset
		remaining = rateLimit.Remaining
	}

	// Check if we have enough API calls remaining
	if available < minRequired {
		timeUntilReset := time.Until(resetTime)

		// If reset is within a reasonable time, sleep until then
		if timeUntilReset > 0 && timeUntilReset < maxSleepUntilReset {
			pterm.Warning.Printf("⚠ %s rate limit low (%d remaining, %d required + %d buffer). Sleeping until reset at %s (%v)\n",
				limitType, remaining, minRequired, rateLimitSafetyBuffer,
				resetTime.Format("15:04:05"), timeUntilReset.Round(time.Second))

			// Add a small buffer to ensure the limit has actually reset
			sleepDuration := timeUntilReset + resetBufferTime
			time.Sleep(sleepDuration)

			pterm.Info.Printf("✓ %s rate limit should be reset, resuming...\n", limitType)

			// Return sentinel error to tell caller to refresh rate limit data
			return ErrRateLimitRefreshNeeded
		}

		// If reset time is too far away or in the past, handle gracefully
		if timeUntilReset < 0 {
			// Reset time is in the past - this can happen due to:
			// - Clock skew between GitHub's servers and client machine
			// - GitHub API bugs or stale data
			// - Timezone issues
			pterm.Warning.Printf("⚠ %s rate limit reset time is in the past (clock skew or API issue).\n", limitType)
			pterm.Info.Printf("  Current time: %s, Reset time: %s\n",
				time.Now().Format("15:04:05"), resetTime.Format("15:04:05"))
			pterm.Info.Println("  Attempting to refresh rate limit data...")

			// Return sentinel error to signal caller to refresh rate limits
			return ErrClockSkewDetected
		}

		return fmt.Errorf("%s rate limit exhausted (%d remaining) and reset is too far away (%v)",
			limitType, remaining, timeUntilReset.Round(time.Minute))
	}

	// Warn if we're getting close to the buffer threshold for either limit
	if rateLimit.Remaining < rateLimitSafetyBuffer*3 {
		pterm.Warning.Printf("⚠ REST rate limit getting low: %d remaining (safety buffer: %d)\n",
			rateLimit.Remaining, rateLimitSafetyBuffer)
	}

	if graphqlLimit.Limit > 0 && graphqlLimit.Remaining < rateLimitSafetyBuffer*3 {
		pterm.Warning.Printf("⚠ GraphQL rate limit getting low: %d remaining (safety buffer: %d)\n",
			graphqlLimit.Remaining, rateLimitSafetyBuffer)
	}

	return nil
}
