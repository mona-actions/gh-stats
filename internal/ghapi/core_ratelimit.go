// Package ghapi provides GitHub API client functionality.
//
// This file (core_ratelimit.go) implements rate limit checking and monitoring for
// GitHub's REST and GraphQL APIs. It periodically fetches rate limit information
// and updates the global state to prevent exceeding API quotas.
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
)

// GetRateLimit fetches the current GitHub API rate limit information.
func GetRateLimit() (*RateLimitResponse, error) {
	cmd := exec.CommandContext(context.Background(), "gh", "api", "rate_limit")
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

// UpdateRateLimitInfo fetches and updates the current rate limit information.
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
