package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mona-actions/gh-stats/internal/stats"
	"github.com/spf13/cobra"
)

var (
	inputFile  string
	orgName    string
	outputFile string
	maxWorkers int
	failFast   bool
	verbose    bool
	resume     bool
	dryRun     bool
	noPackages bool
	hostname   string
	// Feature flags to control which data to fetch
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
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run repo statistics collection",
	Long: `Run statistics collection for one or more GitHub organizations.
	
Examples:
  gh stats run --org mona-actions                 # Creates gh-stats-2025-10-30.json
  gh stats run --input orgs.txt                   # Process multiple organizations
  gh stats run --org mona-actions -w 5 -v         # Enable verbose with 5 workers
  gh stats run --org mona-actions -O custom.json  # Use custom output file
  gh stats run --org mona-actions --no-packages   # Skip package scanning (faster)
  gh stats run --org myorg --hostname github.company.com  # Use with GitHub Enterprise Server
  
The tool automatically resumes from existing output files, skipping already processed repos.
  
Note: Actual usage may vary if additional pagination is needed for large datasets.
Use --no-* flags to reduce API usage (see Data Collection Flags below).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Set default output file with current date unless explicitly specified
		if outputFile == "" {
			outputFile = fmt.Sprintf("gh-stats-%s.json", time.Now().Format("2006-01-02"))
		}

		config := stats.Config{
			OrgName:    orgName,
			InputFile:  inputFile,
			OutputFile: outputFile,
			MaxWorkers: maxWorkers,
			FailFast:   failFast,
			Verbose:    verbose,
			Resume:     resume,
			DryRun:     dryRun,
			NoPackages: noPackages,
			Hostname:   hostname,
			// Feature flags (default to true, disabled by --no-* flags)
			FetchSettings:     !noSettings,
			FetchCustomProps:  !noCustomProps,
			FetchBranches:     !noBranches,
			FetchWebhooks:     !noWebhooks,
			FetchAutolinks:    !noAutolinks,
			FetchActions:      !noActions,
			FetchSecurity:     !noSecurity,
			FetchPages:        !noPages,
			FetchIssuesData:   !noIssuesData,
			FetchPRsData:      !noPRsData,
			FetchTraffic:      !noTraffic,
			FetchTags:         !noTags,
			FetchGitRefs:      !noGitRefs,
			FetchLFS:          !noLFS,
			FetchFiles:        !noFiles,
			FetchContributors: !noContributors,
			FetchCommits:      !noCommits,
			FetchIssueEvents:  !noIssueEvents,
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

// init registers the run command and its flags.
func init() {
	rootCmd.AddCommand(runCmd)

	// Set custom usage template to group flags
	runCmd.SetUsageTemplate(`Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}

Available Commands:{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

General Flags:
  -o, --org string        GitHub organization to analyze
  -i, --input string      File with list of orgs to analyze
  -O, --output string     Output file path (default: gh-stats-YYYY-MM-DD.json)
  -w, --max-workers int   Maximum number of concurrent API calls (default 3)
  -f, --fail-fast         Stop processing on first error
  -v, --verbose           Enable verbose output
  -r, --resume            Resume from existing output file, skipping already processed repositories
      --dry-run           Show what would be collected without making API calls (preview mode)
      --hostname string   GitHub Enterprise Server hostname (e.g., github.company.com)
  -h, --help              help for run

Data Collection Flags (--no-* to disable and reduce API usage):
  High-impact (save multiple API calls):
      --no-packages       Skip fetching package data (faster execution)
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
      --no-webhooks       Skip fetching webhooks{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

Additional help topics:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`)

	runCmd.Flags().StringVarP(&orgName, "org", "o", "", "GitHub organization to analyze")
	runCmd.Flags().StringVarP(&inputFile, "input", "i", "", "File with list of orgs to analyze")
	runCmd.Flags().StringVarP(&outputFile, "output", "O", "", "Output file path (default: gh-stats-YYYY-MM-DD.json)")
	runCmd.Flags().IntVarP(&maxWorkers, "max-workers", "w", 3, "Maximum number of concurrent API calls")
	runCmd.Flags().BoolVarP(&failFast, "fail-fast", "f", false, "Stop processing on first error")
	runCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose output")
	runCmd.Flags().BoolVarP(&resume, "resume", "r", false, "Resume from existing output file, skipping already processed repositories")
	runCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be collected without making API calls (preview mode)")
	runCmd.Flags().StringVar(&hostname, "hostname", "", "GitHub Enterprise Server hostname (e.g., github.company.com)")
	runCmd.Flags().BoolVar(&noPackages, "no-packages", false, "Skip fetching package data (faster execution)")

	// Feature flags to control API usage - disable expensive operations
	// Group these flags together under "Data Collection Flags"
	runCmd.Flags().BoolVar(&noSettings, "no-settings", false, "Skip fetching repository settings (saves ~1 API call/repo)")
	runCmd.Flags().BoolVar(&noCustomProps, "no-custom-props", false, "Skip fetching custom properties (saves ~1 API call/repo)")
	runCmd.Flags().BoolVar(&noBranches, "no-branches", false, "Skip fetching branch details (saves ~1 API call/repo)")
	runCmd.Flags().BoolVar(&noWebhooks, "no-webhooks", false, "Skip fetching webhooks (saves ~1 API call/repo)")
	runCmd.Flags().BoolVar(&noAutolinks, "no-autolinks", false, "Skip fetching autolinks (saves ~1 API call/repo)")
	runCmd.Flags().BoolVar(&noActions, "no-actions", false, "Skip fetching GitHub Actions data (saves ~5 API calls/repo)")
	runCmd.Flags().BoolVar(&noSecurity, "no-security", false, "Skip fetching security data (saves ~7 API calls/repo)")
	runCmd.Flags().BoolVar(&noPages, "no-pages", false, "Skip fetching GitHub Pages data (saves ~1 API call/repo)")
	runCmd.Flags().BoolVar(&noIssuesData, "no-issues-data", false, "Skip fetching detailed issues data (saves ~1 API call/repo)")
	runCmd.Flags().BoolVar(&noPRsData, "no-prs-data", false, "Skip fetching detailed pull request data (saves ~1 API call/repo)")
	runCmd.Flags().BoolVar(&noTraffic, "no-traffic", false, "Skip fetching traffic data (saves ~1 API call/repo)")
	runCmd.Flags().BoolVar(&noTags, "no-tags", false, "Skip fetching detailed tag data (saves ~1 API call/repo)")
	runCmd.Flags().BoolVar(&noGitRefs, "no-git-refs", false, "Skip fetching git references (saves ~1 API call/repo)")
	runCmd.Flags().BoolVar(&noLFS, "no-lfs", false, "Skip fetching Git LFS status (saves ~1 API call/repo)")
	runCmd.Flags().BoolVar(&noFiles, "no-files", false, "Skip fetching repository files (saves ~1 API call/repo)")
	runCmd.Flags().BoolVar(&noContributors, "no-contributors", false, "Skip fetching contributors count (saves ~1 API call/repo)")
	runCmd.Flags().BoolVar(&noCommits, "no-commits", false, "Skip fetching commit count (saves ~1 API call/repo)")
	runCmd.Flags().BoolVar(&noIssueEvents, "no-issue-events", false, "Skip fetching issue events count (saves ~1 API call/repo)")
}
