// gh-stats is a GitHub CLI extension that collects comprehensive statistics
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

func main() {
	cmd.Execute()
}
