// Package output provides formatting and display utilities for terminal output.
//
// This file (formatting.go) contains functions for displaying progress, summaries,
// and metadata in a consistent, visually appealing format using pterm.
//
// Key features:
//   - Styled section headers and organization display
//   - Repository discovery and progress tracking
//   - Batch processing progress visualization
//   - Completion summaries with API statistics
//   - Consistent emoji usage for visual clarity
package output

import (
	"fmt"
	"time"

	"github.com/pterm/pterm"
)

// PrintSectionHeader prints a prominent section header with separator.
func PrintSectionHeader(title string) {
	pterm.Println()
	pterm.DefaultSection.Println(title)
}

// PrintOrgHeader prints the organization header with styling.
func PrintOrgHeader(orgName string) {
	pterm.Println()
	separator := "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	pterm.Info.Println(separator)
	pterm.Info.Printf("🏢 Organization: %s\n", orgName)
	pterm.Info.Println(separator)
	pterm.Println()
}

// OrgMetadataDisplay holds organization metadata for display.
type OrgMetadataDisplay struct {
	SecurityManagers  int
	CustomProperties  int
	Members           int
	OutsideCollabs    int
	Teams             int
	Secrets           int
	Variables         int
	Rulesets          int
	SelfHostedRunners int
	BlockedUsers      int
	Webhooks          int
	SkippedPackages   bool
}

// PrintOrgMetadata prints organization metadata in a tree format.
func PrintOrgMetadata(metadata OrgMetadataDisplay) {
	pterm.Info.Println("📊 Organization Metadata")

	if metadata.SecurityManagers > 0 {
		pterm.Info.Printf("   ├─ 🔒 Security managers: %d\n", metadata.SecurityManagers)
	}
	if metadata.CustomProperties > 0 {
		pterm.Info.Printf("   ├─ 🏷️  Custom properties: %d\n", metadata.CustomProperties)
	}

	pterm.Info.Printf("   ├─ 👥 Members: %d | Outside collaborators: %d\n",
		metadata.Members, metadata.OutsideCollabs)
	pterm.Info.Printf("   ├─ 👥 Teams: %d\n", metadata.Teams)

	if metadata.Secrets > 0 || metadata.Variables > 0 {
		pterm.Info.Printf("   ├─ 🔐 Secrets: %d | Variables: %d\n", metadata.Secrets, metadata.Variables)
	}
	if metadata.Rulesets > 0 {
		pterm.Info.Printf("   ├─ 📋 Rulesets: %d\n", metadata.Rulesets)
	}
	if metadata.Webhooks > 0 {
		pterm.Info.Printf("   ├─ 🔗 Webhooks: %d\n", metadata.Webhooks)
	}
	if metadata.SelfHostedRunners > 0 {
		pterm.Info.Printf("   ├─ 🤖 Self-hosted runners: %d\n", metadata.SelfHostedRunners)
	}
	if metadata.BlockedUsers > 0 {
		pterm.Info.Printf("   └─ 🚫 Blocked users: %d\n", metadata.BlockedUsers)
	} else {
		pterm.Info.Println("   └─ Configuration loaded")
	}

	pterm.Println()
	pterm.Success.Println("✅ Organization metadata saved")
	pterm.Println()
}

// RepoDiscovery holds repository discovery information.
type RepoDiscovery struct {
	Total           int
	New             int
	AlreadyDone     int
	SkippedPackages bool
	SkippedTeams    bool
}

// PrintRepoDiscovery prints repository discovery information.
func PrintRepoDiscovery(info RepoDiscovery) {
	pterm.Info.Println("🔭 Repository Discovery")
	pterm.Info.Printf("   ├─ Found: %d repositories\n", info.Total)

	if info.AlreadyDone > 0 {
		pterm.Info.Printf("   ├─ New: %d | Already processed: %d\n", info.New, info.AlreadyDone)
	} else {
		pterm.Info.Printf("   ├─ New: %d\n", info.New)
	}

	skipped := []string{}
	if info.SkippedPackages {
		skipped = append(skipped, "packages")
	}
	if info.SkippedTeams {
		skipped = append(skipped, "teams")
	}

	if len(skipped) > 0 {
		pterm.Info.Printf("   └─ Skipped: %s (flags set)\n", joinWithComma(skipped))
	} else {
		pterm.Info.Println("   └─ Fetching all optional data")
	}

	pterm.Println()
	pterm.Success.Println("✅ Repository discovery complete")
}

// PrintRepoProcessingHeader prints the repository processing phase header.
func PrintRepoProcessingHeader(count int, mode string) {
	separator := "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	pterm.Info.Println(separator)
	pterm.Info.Printf("📚 Processing %d Repositories (%s)\n", count, mode)
	pterm.Info.Println(separator)
	pterm.Println()
}

// BatchProgress tracks batch fetching progress.
type BatchProgress struct {
	Current   int
	Total     int
	RepoCount int
	Retries   int
	Saved     bool
}

// PrintBatchProgress prints a single batch progress line.
func PrintBatchProgress(batch BatchProgress) {
	prefix := fmt.Sprintf("   ├─ Batch %d/%d (%d repos)", batch.Current, batch.Total, batch.RepoCount)

	if batch.Current == batch.Total {
		prefix = fmt.Sprintf("   └─ Batch %d/%d (%d repos)", batch.Current, batch.Total, batch.RepoCount)
	}

	status := ""
	if batch.Saved {
		status = "✅ Saved"
		if batch.Retries > 0 {
			status = fmt.Sprintf("✅ Saved [%d retry]", batch.Retries)
			if batch.Retries > 1 {
				status = fmt.Sprintf("✅ Saved [%d retries]", batch.Retries)
			}
		}
	} else {
		status = "⏳ Processing..."
	}

	pterm.Info.Printf("%s %s\n", prefix, status)
}

// PrintBatchingComplete prints batch completion message.
func PrintBatchingComplete() {
	pterm.Println()
	pterm.Success.Println("✅ All repository data fetched via GraphQL batches")
}

// PrintProcessingSkipped prints a message when individual processing is skipped.
func PrintProcessingSkipped(reason string) {
	pterm.Println()
	pterm.Info.Printf("⏭️  Individual processing skipped (%s)\n", reason)
	pterm.Println()
}

// CompletionSummary holds the final summary information.
type CompletionSummary struct {
	RepoCount        int
	OutputFile       string
	Duration         time.Duration
	Mode             string
	RESTCalls        int64
	RESTUsed         int64
	RESTLimit        int64
	RESTRemaining    int64
	GraphQLCalls     int
	GraphQLUsed      int64
	GraphQLLimit     int64
	GraphQLRemaining int64
	RESTReset        time.Time
	GraphQLReset     time.Time
	Warnings         int
	Errors           int
}

// PrintCompletionSummary prints the final completion summary.
func PrintCompletionSummary(summary CompletionSummary) {
	pterm.Println()
	separator := "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	pterm.Success.Println(separator)
	pterm.Success.Println("✨ Collection Complete!")
	pterm.Success.Println(separator)
	pterm.Println()

	// Summary
	pterm.Info.Println("📈 Summary")
	pterm.Info.Printf("   ├─ Repositories: %d collected\n", summary.RepoCount)
	pterm.Info.Printf("   ├─ Output file: %s\n", summary.OutputFile)
	pterm.Info.Printf("   ├─ Duration: %s\n", FormatDuration(summary.Duration))
	pterm.Info.Printf("   └─ Mode: %s\n", summary.Mode)
	pterm.Println()

	// API Usage
	pterm.Info.Println("🌐 API Usage")

	// Determine GraphQL batch count (rough estimate)
	batchCount := summary.GraphQLCalls
	if batchCount > 0 {
		pterm.Info.Printf("   ├─ REST: %d calls (%d/%s used, %s remaining)\n",
			summary.RESTCalls,
			summary.RESTUsed,
			FormatNumber(summary.RESTLimit),
			FormatNumber(summary.RESTRemaining))
		pterm.Info.Printf("   └─ GraphQL: ~%d queries (%d/%s points, %s remaining)\n",
			batchCount,
			summary.GraphQLUsed,
			FormatNumber(summary.GraphQLLimit),
			FormatNumber(summary.GraphQLRemaining))
	} else {
		pterm.Info.Printf("   └─ REST: %d calls (%d/%s used, %s remaining)\n",
			summary.RESTCalls,
			summary.RESTUsed,
			FormatNumber(summary.RESTLimit),
			FormatNumber(summary.RESTRemaining))
	}
	pterm.Println()

	// Rate Limits
	pterm.Info.Println("📊 Rate Limits")
	pterm.Info.Printf("   ├─ REST resets: %s (in %s)\n",
		summary.RESTReset.Format("15:04:05"),
		FormatTimeUntil(summary.RESTReset))
	pterm.Info.Printf("   └─ GraphQL resets: %s (in %s)\n",
		summary.GraphQLReset.Format("15:04:05"),
		FormatTimeUntil(summary.GraphQLReset))
	pterm.Println()

	// Warnings/Errors
	if summary.Warnings > 0 || summary.Errors > 0 {
		if summary.Warnings > 0 {
			pterm.Warning.Printf("⚠️  Warnings: %d (check logs for details)\n", summary.Warnings)
		}
		if summary.Errors > 0 {
			pterm.Error.Printf("❌ Errors: %d (check logs for details)\n", summary.Errors)
		}
		pterm.Println()
	}
}

// Helper functions

func joinWithComma(items []string) string {
	if len(items) == 0 {
		return ""
	}
	if len(items) == 1 {
		return items[0]
	}
	result := ""
	for i, item := range items {
		if i > 0 {
			result += ", "
		}
		result += item
	}
	return result
}

// FormatDuration formats a duration in a human-readable way (e.g., "5m30s", "2h15m").
func FormatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	} else if d < time.Hour {
		minutes := int(d.Minutes())
		seconds := int(d.Seconds()) % 60
		if seconds == 0 {
			return fmt.Sprintf("%dm", minutes)
		}
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	if minutes == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh%dm", hours, minutes)
}

// FormatNumber formats a number with thousand separators (e.g., "1,234,567").
func FormatNumber(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return formatWithCommas(n)
}

// formatWithCommas adds thousand separators (commas) to a number.
func formatWithCommas(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}

	var result []byte
	for i, digit := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(digit))
	}
	return string(result)
}

// FormatTimeUntil formats the time until a future time in a human-readable way (e.g., "5m", "2h15m").
func FormatTimeUntil(t time.Time) string {
	d := time.Until(t)
	if d < 0 {
		return "now"
	}

	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	} else if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	if minutes == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh%dm", hours, minutes)
}
