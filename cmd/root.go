// Package cmd provides the command-line interface for gh-stats.
// It defines the Cobra command structure, flag handling, and command execution
// for collecting GitHub repository statistics across organizations.
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "stats",
	Short: "Gather GitHub repository statistics for organizations",
	Long: `gh stats is a GitHub CLI extension that collects and reports 
statistics about repositories in one or more organizations.`,
	Run: func(cmd *cobra.Command, args []string) {
		// fallback message, stats logic is in a subcommand
		fmt.Println("Use `gh stats run` to start collecting statistics.")
	},
}

func Execute() {
	cobra.CheckErr(rootCmd.Execute())
}
