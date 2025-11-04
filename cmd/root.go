// Package cmd provides the command-line interface for gh stats.
// It defines the Cobra command structure, flag handling, and command execution
// for collecting GitHub repository statistics across organizations.
package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mona-actions/gh-stats/internal/ghapi"
	"github.com/mona-actions/gh-stats/internal/stats"
	"github.com/spf13/cobra"
)

// Version is the current version of gh stats, set by main package at startup.
var Version = "dev"

var (
	inputFile        string
	orgName          string
	outputFile       string
	maxWorkers       int
	graphQLBatchSize int
	failFast         bool
	verbose          bool
	dryRun           bool
	minimal          bool
	noPackages       bool
	// Feature flags to control which data to fetch.
	noSettings     bool
	noCustomProps  bool
	noBranches     bool
	noWebhooks     bool
	noAutolinks    bool
	noActions      bool
	noSecurity     bool
	noPages        bool
	noIssuesData   bool
	noPRsData      bool
	noTraffic      bool
	noTags         bool
	noGitRefs      bool
	noLFS          bool
	noFiles        bool
	noContributors bool
	noCommits      bool
	noIssueEvents  bool
	// GraphQL-specific feature flags.
	noCollaborators    bool
	noLanguages        bool
	noTopics           bool
	noLicense          bool
	noDeployKeys       bool
	noEnvironments     bool
	noDeployments      bool
	noMilestones       bool
	noReleases         bool
	noCommunityFiles   bool
	noRulesets         bool
	noBranchProtection bool
	noTeams            bool
)

var rootCmd = &cobra.Command{
	Use:   "stats",
	Short: "Gather GitHub repository statistics for organizations",
	Long: `gh stats is a GitHub CLI extension that collects and reports 
statistics about repositories in one or more organizations.

Examples:
  gh stats --org mona-actions                 # Creates gh-stats-2025-10-30.json
  gh stats --input orgs.txt                   # Process multiple organizations
  gh stats --org mona-actions -w 5 -v         # Enable verbose with 5 workers
  gh stats --org mona-actions -O custom.json  # Use custom output file
  gh stats --org mona-actions --no-packages   # Skip package scanning (faster)
  
The tool automatically resumes from existing output files, skipping already processed repos.
  
Note: Actual usage may vary if additional pagination is needed for large datasets.
Use --no-* flags to reduce API usage (see Data Collection Flags below).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Set default output file with current date unless explicitly specified
		if outputFile == "" {
			outputFile = fmt.Sprintf("gh-stats-%s.json", time.Now().Format("2006-01-02"))
		}

		// If --minimal is set, enable all no-* flags unless explicitly overridden
		if minimal {
			flags := []struct {
				name string
				ptr  *bool
			}{
				{"no-packages", &noPackages},
				{"no-teams", &noTeams},
				{"no-settings", &noSettings},
				{"no-custom-props", &noCustomProps},
				{"no-branches", &noBranches},
				{"no-webhooks", &noWebhooks},
				{"no-autolinks", &noAutolinks},
				{"no-actions", &noActions},
				{"no-security", &noSecurity},
				{"no-pages", &noPages},
				{"no-issues-data", &noIssuesData},
				{"no-prs-data", &noPRsData},
				{"no-traffic", &noTraffic},
				{"no-tags", &noTags},
				{"no-git-refs", &noGitRefs},
				{"no-lfs", &noLFS},
				{"no-files", &noFiles},
				{"no-contributors", &noContributors},
				{"no-commits", &noCommits},
				{"no-issue-events", &noIssueEvents},
				{"no-collaborators", &noCollaborators},
				{"no-languages", &noLanguages},
				{"no-topics", &noTopics},
				{"no-license", &noLicense},
				{"no-deploy-keys", &noDeployKeys},
				{"no-environments", &noEnvironments},
				{"no-deployments", &noDeployments},
				{"no-milestones", &noMilestones},
				{"no-releases", &noReleases},
				{"no-community-files", &noCommunityFiles},
				{"no-rulesets", &noRulesets},
				{"no-branch-protection", &noBranchProtection},
			}

			for _, f := range flags {
				if !cmd.Flags().Changed(f.name) {
					*f.ptr = true
				}
			}
		}

		config := stats.Config{
			OrgName:          orgName,
			InputFile:        inputFile,
			OutputFile:       outputFile,
			Version:          Version,
			MaxWorkers:       maxWorkers,
			GraphQLBatchSize: graphQLBatchSize,
			FailFast:         failFast,
			Verbose:          verbose,
			DryRun:           dryRun,
			NoPackages:       noPackages,
			NoTeams:          noTeams,
			// Populate embedded DataFetchConfig
			DataFetchConfig: ghapi.DataFetchConfig{
				FetchSettings:         !noSettings,
				FetchCustomProps:      !noCustomProps,
				FetchBranches:         !noBranches,
				FetchWebhooks:         !noWebhooks,
				FetchAutolinks:        !noAutolinks,
				FetchActions:          !noActions,
				FetchSecurity:         !noSecurity,
				FetchPages:            !noPages,
				FetchIssuesData:       !noIssuesData,
				FetchPRsData:          !noPRsData,
				FetchTraffic:          !noTraffic,
				FetchTags:             !noTags,
				FetchGitRefs:          !noGitRefs,
				FetchLFS:              !noLFS,
				FetchFiles:            !noFiles,
				FetchContributors:     !noContributors,
				FetchCommits:          !noCommits,
				FetchIssueEvents:      !noIssueEvents,
				FetchCollaborators:    !noCollaborators,
				FetchLanguages:        !noLanguages,
				FetchTopics:           !noTopics,
				FetchLicense:          !noLicense,
				FetchDeployKeys:       !noDeployKeys,
				FetchEnvironments:     !noEnvironments,
				FetchDeployments:      !noDeployments,
				FetchMilestones:       !noMilestones,
				FetchReleases:         !noReleases,
				FetchCommunityFiles:   !noCommunityFiles,
				FetchRulesets:         !noRulesets,
				FetchBranchProtection: !noBranchProtection,
			},
		}

		// Set up context with timeout and signal handling
		// 24-hour timeout prevents indefinite hangs if GitHub API becomes unresponsive
		ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
		defer cancel()

		// Handle interrupt signals
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

		go func() {
			// Wait for first signal
			sig := <-sigChan

			// Provide context about what signal was received
			if sig == syscall.SIGTERM {
				fmt.Fprintln(os.Stderr, "\nReceived termination signal (SIGTERM), shutting down gracefully...")
			} else {
				fmt.Fprintln(os.Stderr, "\nReceived interrupt signal, shutting down gracefully... (press Ctrl-C again to force quit)")
			}
			cancel()

			// For SIGTERM (from timeout/systemd), don't wait for second signal - just exit gracefully
			if sig == syscall.SIGTERM {
				return
			}

			// For SIGINT (Ctrl-C), wait for second signal to force quit
			<-sigChan
			fmt.Fprintln(os.Stderr, "\nForce quitting...")
			os.Exit(130) // Standard exit code for SIGINT
		}()

		return stats.RunWithContext(ctx, config)
	},
}

// Execute runs the root command and handles execution errors.
// This is the main entry point for the CLI application.
// If the command fails, the error is handled by cobra.CheckErr which exits with status code 1.
func Execute() {
	cobra.CheckErr(rootCmd.Execute())
}

func init() {
	// Set custom usage template to group flags
	rootCmd.SetUsageTemplate(`Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}

Available Commands:{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
  -o, --org string              Single organization to analyze (mutually exclusive with --input)
  -i, --input string            File containing organization names, one per line (mutually exclusive with --org)
  -O, --output string           Output file path (default: gh-stats-YYYY-MM-DD.json)
  -w, --max-workers int         Number of repositories to process concurrently (default: 3)
      --graphql-batch-size int  Repositories per GraphQL batch query (default: 20, range: 1-50)
  -f, --fail-fast               Stop immediately on first error instead of continuing
  -v, --verbose                 Display detailed progress and debug information
      --dry-run                 Preview mode: show what would be collected without making API calls
      --minimal                 Fast mode: collect only base GraphQL data (~1 call per 50 repos vs ~30-40 per repo)
  -h, --help                    Display this help message

Data Collection Flags (--no-* to disable and reduce API usage):
  High-impact (save multiple API calls):
      --no-packages       Skip fetching package data (faster execution)
      --no-teams          Skip fetching team access data (saves ~50 API calls/team in large orgs)
      --no-security       Skip fetching security data (saves 2-4 API calls/repo)
      --no-actions        Skip fetching GitHub Actions data (saves ~5 API calls/repo)
  
  Individual features (save ~1 API call each):
      --no-autolinks      Skip fetching autolinks
      --no-branches       Skip fetching branch details
      --no-commits        Skip fetching commit count
      --no-contributors   Skip fetching contributors count
      --no-custom-props   Skip fetching custom properties
      --no-files          Skip fetching repository files
      --no-git-refs       Skip fetching git references
      --no-issue-events   Skip fetching issue events count
      --no-issues-data    Skip fetching detailed issues data
      --no-lfs            Skip fetching Git LFS status
      --no-pages          Skip fetching GitHub Pages data
      --no-prs-data       Skip fetching detailed pull request data
      --no-settings       Skip fetching repository settings
      --no-tags           Skip fetching detailed tag data
      --no-traffic        Skip fetching traffic data
      --no-webhooks       Skip fetching webhooks

  GraphQL complexity control (reduce query size for large repos):
      --no-collaborators      Skip fetching collaborators list
      --no-languages          Skip fetching language breakdown
      --no-topics             Skip fetching repository topics
      --no-license            Skip fetching license information
      --no-deploy-keys        Skip fetching deploy keys
      --no-environments       Skip fetching environments
      --no-deployments        Skip fetching deployment history
      --no-milestones         Skip fetching milestones
      --no-releases           Skip fetching releases
      --no-community-files    Skip fetching community files
      --no-rulesets           Skip fetching repository rulesets
      --no-branch-protection  Skip fetching branch protection rules{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

Additional help topics:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`)

	rootCmd.Flags().StringVarP(&orgName, "org", "o", "", "Single organization to analyze")
	rootCmd.Flags().StringVarP(&inputFile, "input", "i", "", "File containing organization names, one per line")
	rootCmd.Flags().StringVarP(&outputFile, "output", "O", "", "Output file path (default: gh-stats-YYYY-MM-DD.json)")
	rootCmd.Flags().IntVarP(&maxWorkers, "max-workers", "w", 3, "Number of repositories to process concurrently")
	rootCmd.Flags().IntVar(&graphQLBatchSize, "graphql-batch-size", 20, "Repositories per GraphQL batch query (1-50)")
	rootCmd.Flags().BoolVarP(&failFast, "fail-fast", "f", false, "Stop immediately on first error")
	rootCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Display detailed progress and debug information")
	rootCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview mode: show what would be collected without making API calls")
	rootCmd.Flags().BoolVar(&minimal, "minimal", false, "Fast mode: collect only base GraphQL data (~1 call per 50 repos)")
	rootCmd.Flags().BoolVar(&noPackages, "no-packages", false, "Skip fetching package data (faster execution)")
	rootCmd.Flags().BoolVar(&noTeams, "no-teams", false, "Skip fetching team access data (significant API savings for large orgs)")

	// Feature flags to control API usage - disable expensive operations
	// Group these flags together under "Data Collection Flags"
	rootCmd.Flags().BoolVar(&noSettings, "no-settings", false, "Skip fetching repository settings (saves ~1 API call/repo)")
	rootCmd.Flags().BoolVar(&noCustomProps, "no-custom-props", false, "Skip fetching custom properties (saves ~1 API call/repo)")
	rootCmd.Flags().BoolVar(&noBranches, "no-branches", false, "Skip fetching branch details (saves ~1 API call/repo)")
	rootCmd.Flags().BoolVar(&noWebhooks, "no-webhooks", false, "Skip fetching webhooks (saves ~1 API call/repo)")
	rootCmd.Flags().BoolVar(&noAutolinks, "no-autolinks", false, "Skip fetching autolinks (saves ~1 API call/repo)")
	rootCmd.Flags().BoolVar(&noActions, "no-actions", false, "Skip fetching GitHub Actions data (saves ~5 API calls/repo)")
	rootCmd.Flags().BoolVar(&noSecurity, "no-security", false, "Skip fetching security data (saves ~7 API calls/repo)")
	rootCmd.Flags().BoolVar(&noPages, "no-pages", false, "Skip fetching GitHub Pages data (saves ~1 API call/repo)")
	rootCmd.Flags().BoolVar(&noIssuesData, "no-issues-data", false, "Skip fetching detailed issues data (saves ~1 API call/repo)")
	rootCmd.Flags().BoolVar(&noPRsData, "no-prs-data", false, "Skip fetching detailed pull request data (saves ~1 API call/repo)")
	rootCmd.Flags().BoolVar(&noTraffic, "no-traffic", false, "Skip fetching traffic data (saves ~1 API call/repo)")
	rootCmd.Flags().BoolVar(&noTags, "no-tags", false, "Skip fetching detailed tag data (saves ~1 API call/repo)")
	rootCmd.Flags().BoolVar(&noGitRefs, "no-git-refs", false, "Skip fetching git references (saves ~1 API call/repo)")
	rootCmd.Flags().BoolVar(&noLFS, "no-lfs", false, "Skip fetching Git LFS status (saves ~1 API call/repo)")
	rootCmd.Flags().BoolVar(&noFiles, "no-files", false, "Skip fetching repository files (saves ~1 API call/repo)")
	rootCmd.Flags().BoolVar(&noContributors, "no-contributors", false, "Skip fetching contributors count (saves ~1 API call/repo)")
	rootCmd.Flags().BoolVar(&noCommits, "no-commits", false, "Skip fetching commit count (saves ~1 API call/repo)")
	rootCmd.Flags().BoolVar(&noIssueEvents, "no-issue-events", false, "Skip fetching issue events count (saves ~1 API call/repo)")

	// GraphQL-specific flags - control query complexity and response size
	rootCmd.Flags().BoolVar(&noCollaborators, "no-collaborators", false, "Skip fetching collaborators (reduces GraphQL complexity)")
	rootCmd.Flags().BoolVar(&noLanguages, "no-languages", false, "Skip fetching language breakdown (reduces GraphQL complexity)")
	rootCmd.Flags().BoolVar(&noTopics, "no-topics", false, "Skip fetching repository topics (reduces GraphQL complexity)")
	rootCmd.Flags().BoolVar(&noLicense, "no-license", false, "Skip fetching license information (reduces GraphQL complexity)")
	rootCmd.Flags().BoolVar(&noDeployKeys, "no-deploy-keys", false, "Skip fetching deploy keys (reduces GraphQL complexity)")
	rootCmd.Flags().BoolVar(&noEnvironments, "no-environments", false, "Skip fetching environments (reduces GraphQL complexity)")
	rootCmd.Flags().BoolVar(&noDeployments, "no-deployments", false, "Skip fetching deployments (reduces GraphQL complexity)")
	rootCmd.Flags().BoolVar(&noMilestones, "no-milestones", false, "Skip fetching milestones (reduces GraphQL complexity)")
	rootCmd.Flags().BoolVar(&noReleases, "no-releases", false, "Skip fetching releases (reduces GraphQL complexity)")
	rootCmd.Flags().BoolVar(&noCommunityFiles, "no-community-files", false, "Skip fetching community files (reduces GraphQL complexity)")
	rootCmd.Flags().BoolVar(&noRulesets, "no-rulesets", false, "Skip fetching repository rulesets (reduces GraphQL complexity)")
	rootCmd.Flags().BoolVar(&noBranchProtection, "no-branch-protection", false, "Skip fetching branch protection rules (reduces GraphQL complexity)")
}
