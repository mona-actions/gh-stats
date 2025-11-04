// Package ghapi provides GitHub API client functionality.
//
// This file (orgs_packages.go) contains package fetching operations for GitHub Packages.
// It handles concurrent fetching of packages across different package types (npm, maven, etc.)
// with progress tracking and resume support.
//
// Key features:
//   - Concurrent package fetching across multiple package types
//   - Progress bar with type rotation display
//   - Resume support from existing package data
//   - Graceful error handling (non-critical failures don't stop processing)
//   - Package count and version count tracking
package ghapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mona-actions/gh-stats/internal/output"
	"github.com/pterm/pterm"
)

// startPackageProgressBar cycles the progress bar title through remaining package types until done is closed.
// Intended to be run as a goroutine. WaitGroup is used for synchronization.
func startPackageProgressBar(progress *pterm.ProgressbarPrinter, done <-chan struct{}, completed <-chan string, wg *sync.WaitGroup) {
	defer wg.Done()

	// Track which package types have been completed
	completedTypes := make(map[string]bool)
	remainingTypes := make([]string, len(SupportedPackageTypes))
	copy(remainingTypes, SupportedPackageTypes)

	// Simple counter for cycling through remaining types
	counter := 0

	for {
		select {
		case <-done:
			progress.UpdateTitle("✅ Package fetching complete")
			time.Sleep(DoneMessageDisplayTime)
			_, _ = progress.Stop() // Error is not critical for UI cleanup
			// Completion message is printed in processor.go after summary
			return
		case completedType := <-completed:
			// Mark this package type as completed
			completedTypes[completedType] = true

			// Update remaining types list
			remainingTypes = remainingTypes[:0]
			for _, pkgType := range SupportedPackageTypes {
				if !completedTypes[pkgType] {
					remainingTypes = append(remainingTypes, pkgType)
				}
			}
		default:
			// Show remaining package types in rotation
			if len(remainingTypes) > 0 {
				// Use a simple counter to cycle through remaining types
				index := counter % len(remainingTypes)
				pkgType := remainingTypes[index]
				progress.UpdateTitle(fmt.Sprintf("Fetching %s... (%d remaining)", pkgType, len(remainingTypes)))
				counter++
			} else {
				progress.UpdateTitle("Processing package data...")
			}
			time.Sleep(PackageTypeCycleInterval)
		}
	}
}

// fetchPackagesForType fetches all packages of a given type for an org using the REST API.
func fetchPackagesForType(ctx context.Context, org, pkgType string, verbose bool) ([]PackageResponse, error) {
	endpoint := fmt.Sprintf("/orgs/%s/packages?package_type=%s", org, pkgType)

	// Use the generic REST pagination function from rest.go
	output, err := executeRESTPaginatedSimple(ctx, endpoint, verbose)
	if err != nil {
		return nil, err
	}

	// Parse the paginated response into PackageResponse objects
	return parsePackageResponse(output, verbose)
}

// parsePackageResponse parses the paginated REST API response into PackageResponse objects.
// The response from --paginate is JSON objects/arrays separated by newlines.
func parsePackageResponse(output string, verbose bool) ([]PackageResponse, error) {
	if strings.TrimSpace(output) == "" {
		// Empty response (no packages)
		return []PackageResponse{}, nil
	}

	// gh api --paginate returns each page as a separate JSON on a line
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var allResults []PackageResponse

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Try to parse as array first (most common)
		var arrayResults []PackageResponse
		if err := json.Unmarshal([]byte(line), &arrayResults); err == nil {
			allResults = append(allResults, arrayResults...)
			continue
		}

		// Try as single object
		var singleResult PackageResponse
		if err := json.Unmarshal([]byte(line), &singleResult); err == nil {
			allResults = append(allResults, singleResult)
			continue
		}

		// Failed to parse this line
		if verbose {
			pterm.Warning.Printf("Failed to parse package response line: %s\n", truncateString(line, 100))
		}
	}

	return allResults, nil
}

// truncateString truncates a string to maxLen characters, adding "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// convertToPackageData converts PackageResponse to PackageData for output.
func convertToPackageData(org string, results []PackageResponse) []output.PackageData {
	var packageData []output.PackageData
	for _, pkg := range results {
		repoName := pkg.Repository.Name
		if repoName == "" {
			repoName = "unassigned" // Packages not associated with a specific repository
		}

		packageData = append(packageData, output.PackageData{
			Org:          org,
			Repo:         repoName,
			PackageName:  pkg.Name,
			PackageType:  pkg.Type,
			Visibility:   pkg.Visibility,
			CreatedAt:    pkg.CreatedAt,
			UpdatedAt:    pkg.UpdatedAt,
			VersionCount: pkg.VersionCount,
		})
	}
	return packageData
}

// useExistingPackageData returns existing package data if available.
// Returns the counts, empty package data slice, and true if existing data was used.
func useExistingPackageData(org string, progress *pterm.ProgressbarPrinter, existingCounts map[string]output.PackageCounts) (map[string]output.PackageCounts, []output.PackageData, bool) {
	if len(existingCounts) == 0 {
		return nil, nil, false
	}

	if progress != nil {
		progress.UpdateTitle("Using existing package data")
		for i := 0; i < len(SupportedPackageTypes); i++ {
			progress.Increment()
		}
		_, _ = progress.Stop() // Error is not critical for UI cleanup
	}
	pterm.Info.Printf("Using existing package data for %s (%d repositories)\n", org, len(existingCounts))
	return existingCounts, []output.PackageData{}, true
}

// fetchPackagesWorker fetches packages for a single type and aggregates results.
// This function is designed to be run as a goroutine with proper synchronization.
func fetchPackagesWorker(ctx context.Context, org, pkgType string, verbose bool, progress *pterm.ProgressbarPrinter,
	repoPackages map[string]map[string]output.PackageData, countsMu *sync.Mutex,
	allPackageData *[]output.PackageData, packageDataMu *sync.Mutex,
	completed chan<- string) {

	results, err := fetchPackagesForType(ctx, org, pkgType, verbose)
	if err == nil {
		countsMu.Lock()
		// Aggregate packages with full data
		for _, pkg := range results {
			repoName := pkg.Repository.Name
			if repoName == "" {
				repoName = "unassigned" // Packages not associated with a specific repository
			}

			if _, ok := repoPackages[repoName]; !ok {
				repoPackages[repoName] = make(map[string]output.PackageData)
			}
			repoPackages[repoName][pkg.Name] = output.PackageData{
				Org:          org,
				Repo:         repoName,
				PackageName:  pkg.Name,
				PackageType:  pkg.Type,
				Visibility:   pkg.Visibility,
				CreatedAt:    pkg.CreatedAt,
				UpdatedAt:    pkg.UpdatedAt,
				VersionCount: pkg.VersionCount,
			}
		}
		countsMu.Unlock()

		// Collect all package data
		packageDataMu.Lock()
		*allPackageData = append(*allPackageData, convertToPackageData(org, results)...)
		packageDataMu.Unlock()

		if progress != nil {
			progress.Increment()
		}

		// Notify that this package type is completed
		select {
		case completed <- pkgType:
		default:
		}
	} else {
		// Log warning but don't fail - package errors are non-critical
		if verbose {
			pterm.Warning.Printf("Failed to fetch %s packages for %s (continuing): %v\n", pkgType, org, err)
		}
		// Mark as completed even if failed, so progress bar updates
		if progress != nil {
			progress.Increment()
		}
		select {
		case completed <- pkgType:
		default:
		}
	}
}

// convertRepoPackagesToCounts converts aggregated package data to PackageCounts.
// Uses "org/repo" format as the key to match what's used in repository.go.
func convertRepoPackagesToCounts(org string, repoPackages map[string]map[string]output.PackageData) map[string]output.PackageCounts {
	counts := make(map[string]output.PackageCounts)
	for repo, pkgs := range repoPackages {
		packageCount := len(pkgs)
		versionCount := 0

		// Sum up version counts for all packages in this repository
		for _, pkg := range pkgs {
			if pkg.VersionCount != nil {
				versionCount += *pkg.VersionCount
			}
		}

		// Use "org/repo" format as the key to match what's used in repository.go
		repoKey := fmt.Sprintf("%s/%s", org, repo)
		counts[repoKey] = output.PackageCounts{
			PackageCount:    packageCount,
			PackageVersions: versionCount,
		}
	}
	return counts
}

// GetOrgPackageDataWithResume fetches package data for an organization with resume support.
// It can resume from previously collected package counts to avoid re-fetching data,
// making it suitable for interrupted operations or incremental updates.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - org: Organization name
//   - progress: Progress bar for visual feedback (may be nil)
//   - existingCounts: Previously collected package counts for resume functionality
//   - verbose: Enable verbose logging for debugging
//
// Returns:
//   - Map of repository name -> package counts
//   - Slice of detailed package data
//   - Error if fetching fails
//
// The function fetches packages concurrently across different package types (npm, maven, etc.)
// and can be cancelled via context. If existing counts are provided and valid, those are returned
// immediately without making any API calls.
func GetOrgPackageDataWithResume(ctx context.Context, org string, progress *pterm.ProgressbarPrinter, existingCounts map[string]output.PackageCounts, verbose bool) (map[string]output.PackageCounts, []output.PackageData, error) {
	// If we have existing counts and they're not empty, use them
	if counts, data, ok := useExistingPackageData(org, progress, existingCounts); ok {
		return counts, data, nil
	}

	repoPackages := make(map[string]map[string]output.PackageData)
	var countsMu sync.Mutex
	var allPackageData []output.PackageData
	var packageDataMu sync.Mutex

	done := make(chan struct{})
	completed := make(chan string, len(SupportedPackageTypes))
	var cyclingWG sync.WaitGroup
	if progress != nil {
		cyclingWG.Add(1)
		go startPackageProgressBar(progress, done, completed, &cyclingWG)
	}

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, MaxConcurrentPackageFetches)

	for _, pkgType := range SupportedPackageTypes {
		wg.Add(1)
		go func(pkgType string) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			fetchPackagesWorker(ctx, org, pkgType, verbose, progress, repoPackages, &countsMu, &allPackageData, &packageDataMu, completed)
		}(pkgType)
	}

	wg.Wait()
	close(done)
	cyclingWG.Wait()

	counts := convertRepoPackagesToCounts(org, repoPackages)
	return counts, allPackageData, nil
}
