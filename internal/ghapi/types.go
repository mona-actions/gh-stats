// Package ghapi provides GitHub API client functionality.
//
// This file (types.go) defines common types, constants, and data structures used
// across the ghapi package for interacting with GitHub's REST and GraphQL APIs.
package ghapi

import "time"

// Package types supported by GitHub.
const (
	PackageTypeContainer = "container"
	PackageTypeNPM       = "npm"
	PackageTypeMaven     = "maven"
	PackageTypeRubyGems  = "rubygems"
	PackageTypeNuGet     = "nuget"
	PackageTypeDocker    = "docker"
)

// Constants for configuration.
const (
	MaxConcurrentPackageFetches = 2
	PackageTypeCycleInterval    = 500 * time.Millisecond
	DoneMessageDisplayTime      = 1200 * time.Millisecond
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
