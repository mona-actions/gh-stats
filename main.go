// gh stats is a GitHub CLI extension that collects comprehensive statistics
// about repositories in GitHub organizations. It provides detailed metrics
// including repository metadata, collaborators, security settings, packages,
// and more.
//
// Usage:
//
//	gh stats run --org myorg
//	gh stats run --input orgs.txt --max-workers 5
//
// For full documentation, see: https://github.com/mona-actions/gh-stats
package main

import (
	"github.com/mona-actions/gh-stats/cmd"
)

// Version is the current version of gh stats.
// It can be overridden at build time using:
//
//	go build -ldflags="-X main.Version=v1.0.0"
//
// During releases, this is automatically set from the git tag.
var Version = "dev"

func main() {
	// Set version in cmd package so it can be accessed by subcommands
	cmd.Version = Version
	cmd.Execute()
}
