// Package output provides JSON output functionality for repository statistics and package data.
//
// This package handles all file I/O operations for the gh-stats tool, including writing
// consolidated JSON output, reading existing data for resume operations, and managing
// incremental updates to output files.
//
// Key Features:
//   - Consolidated JSON output with orgs, repos, and packages
//   - Atomic file writes (temp file + fsync + rename) to prevent corruption
//   - Thread-safe file operations with mutex protection
//   - Incremental updates with duplicate detection
//   - Resume capability by reading existing files
//   - Pretty-printed JSON for readability
//
// Basic Usage:
//
//	// Writing consolidated data
//	stats := output.ConsolidatedStats{
//	    Orgs:     []output.OrgMetadata{...},
//	    Repos:    []output.RepoStats{...},
//	    Packages: []output.PackageData{...},
//	}
//	err := output.WriteConsolidatedJSON("stats.json", stats)
//
//	// Appending new data incrementally
//	newOrgs := []output.OrgMetadata{...}
//	newRepos := []output.RepoStats{...}
//	err := output.AppendToConsolidatedJSON("stats.json", newOrgs, newRepos, nil)
//
//	// Reading existing data for resume
//	existing, err := output.ReadExistingJSON("stats.json")
//	if err != nil {
//	    // Handle error or start fresh
//	}
//	// existing is a map[string]RepoStats keyed by "org/repo"
//
// File Format:
//
// The consolidated JSON output has the following structure:
//
//	{
//	  "orgs": [
//	    {
//	      "login": "my-org",
//	      "name": "My Organization",
//	      ...
//	    }
//	  ],
//	  "repos": [
//	    {
//	      "org": "my-org",
//	      "name": "my-repo",
//	      ...
//	    }
//	  ],
//	  "packages": [
//	    {
//	      "org": "my-org",
//	      "repo": "my-repo",
//	      "packageName": "my-package",
//	      ...
//	    }
//	  ]
//	}
package output

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// fileMu serializes all file write operations to prevent concurrent writes
// from multiple goroutines from corrupting the output file.
// This mutex protects both WriteConsolidatedJSON and AppendToConsolidatedJSON.
var fileMu sync.Mutex

// cachedConsolidated holds cached consolidated stats alongside lookup maps so we can
// reuse allocations across incremental writes. Access to this cache must occur while
// fileMu is held to keep the in-memory state consistent with on-disk data.
type cachedConsolidated struct {
	stats      ConsolidatedStats
	orgMap     map[string]OrgMetadata
	repoMap    map[string]RepoStats
	packageMap map[string]PackageData
}

var consolidatedCache = make(map[string]*cachedConsolidated)

// WriteConsolidatedJSON writes the consolidated statistics to a JSON file at filePath atomically.
// The output is a pretty-printed JSON object with orgs, repos, and packages.
// Uses atomic write (temp file + rename) to prevent data loss if the process crashes during write.
// This function is thread-safe and can be called from multiple goroutines.
func WriteConsolidatedJSON(filePath string, stats ConsolidatedStats) (err error) {
	// Serialize file writes to prevent concurrent access from multiple goroutines
	fileMu.Lock()
	defer fileMu.Unlock()

	return writeConsolidatedJSONInternal(filePath, stats)
}

// writeConsolidatedJSONInternal performs the actual atomic file write without locking.
// This is used internally when the lock is already held (e.g., from AppendToConsolidatedJSON).
func writeConsolidatedJSONInternal(filePath string, stats ConsolidatedStats) (err error) {
	// 1. Write to temporary file first
	tmpFile := filePath + ".tmp"
	file, err := os.Create(tmpFile)
	if err != nil {
		return fmt.Errorf("failed to create temp file %s: %w", tmpFile, err)
	}

	// Ensure cleanup on error
	defer func() {
		if err != nil {
			_ = file.Close()       // Error not critical during error cleanup
			_ = os.Remove(tmpFile) // Error not critical during error cleanup
		}
	}()

	// 2. Write JSON data
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ") // Pretty print with 2-space indentation

	if err = encoder.Encode(stats); err != nil {
		return fmt.Errorf("failed to write JSON to %s: %w", tmpFile, err)
	}

	// 3. Flush and sync to disk to ensure data is written
	if err = file.Sync(); err != nil {
		return fmt.Errorf("failed to sync file %s: %w", tmpFile, err)
	}

	// 4. Close the file
	if err = file.Close(); err != nil {
		return fmt.Errorf("failed to close file %s: %w", tmpFile, err)
	}

	// 5. Atomic rename (POSIX guarantees atomicity)
	// If this succeeds, the old file is replaced atomically
	if err = os.Rename(tmpFile, filePath); err != nil {
		_ = os.Remove(tmpFile) // Error not critical during error cleanup
		return fmt.Errorf("failed to rename temp file to %s: %w", filePath, err)
	}

	return nil
}

// AppendToConsolidatedJSON appends new data to an existing consolidated JSON file.
//
// This function provides incremental file updates for long-running collection processes.
// It reads the existing consolidated JSON file, merges new data (avoiding duplicates),
// and writes the updated data back to disk. If the file doesn't exist, creates a new one.
//
// This function is thread-safe and uses atomic writes to prevent data corruption.
//
// Parameters:
//   - filePath: Path to the consolidated JSON output file
//   - orgs: New organization metadata to append/update (nil-safe)
//   - repos: New repository statistics to append/update (nil-safe)
//   - packages: New package data to append/update (nil-safe)
//
// Duplicate Handling:
//   - Organizations: Keyed by Login (updates replace existing)
//   - Repositories: Keyed by "Org/Name" (updates replace existing)
//   - Packages: Keyed by "Org/Repo/PackageName/PackageType" (updates replace existing)
//
// Returns:
//   - nil on success
//   - read error if existing file exists but cannot be parsed or read
//   - write error if file cannot be written to disk
//   - JSON marshal error if data cannot be converted to JSON
//
// Possible Errors:
//   - os.ErrPermission if file permissions prevent read/write
//   - os.ErrNotExist if parent directory doesn't exist
//   - json.SyntaxError if existing file contains malformed JSON
//   - io.ErrShortWrite if disk is full during write
//   - json.UnsupportedTypeError if data contains unmarshalable types
//
// Performance:
//   - O(n) complexity where n = total items across all collections
//   - Full file read + parse + write on every call
//   - Not optimized for very large datasets (10,000+ items)
func AppendToConsolidatedJSON(filePath string, orgs []OrgMetadata, repos []RepoStats, packages []PackageData) error {
	// Serialize all file operations to prevent concurrent writes
	// This protects the read-modify-write sequence from race conditions
	fileMu.Lock()
	defer fileMu.Unlock()

	entry, err := loadCachedConsolidatedLocked(filePath)
	if err != nil {
		return fmt.Errorf("failed to load consolidated JSON: %w", err)
	}

	for _, org := range orgs {
		entry.orgMap[org.Login] = org
	}
	for _, repo := range repos {
		key := fmt.Sprintf("%s/%s", repo.Org, repo.Name)
		entry.repoMap[key] = repo
	}
	for _, pkg := range packages {
		key := fmt.Sprintf("%s/%s/%s/%s", pkg.Org, pkg.Repo, pkg.PackageName, pkg.PackageType)
		entry.packageMap[key] = pkg
	}

	newStats := ConsolidatedStats{
		Orgs:     make([]OrgMetadata, 0, len(entry.orgMap)),
		Repos:    make([]RepoStats, 0, len(entry.repoMap)),
		Packages: make([]PackageData, 0, len(entry.packageMap)),
	}

	for _, org := range entry.orgMap {
		newStats.Orgs = append(newStats.Orgs, org)
	}
	for _, repo := range entry.repoMap {
		newStats.Repos = append(newStats.Repos, repo)
	}
	for _, pkg := range entry.packageMap {
		newStats.Packages = append(newStats.Packages, pkg)
	}

	if err := writeConsolidatedJSONInternal(filePath, newStats); err != nil {
		return err
	}

	entry.stats = newStats
	return nil
}

// ReadConsolidatedJSON reads an existing consolidated JSON file.
// Returns an empty ConsolidatedStats if the file doesn't exist or can't be read.
func ReadConsolidatedJSON(filePath string) (ConsolidatedStats, error) {
	return readConsolidatedJSONFromDisk(filePath)
}

// loadCachedConsolidatedLocked returns a cached consolidated stats entry, loading it
// from disk if necessary. fileMu must be held by the caller.
func loadCachedConsolidatedLocked(filePath string) (*cachedConsolidated, error) {
	if entry, ok := consolidatedCache[filePath]; ok {
		return entry, nil
	}

	stats, err := readConsolidatedJSONFromDisk(filePath)
	if err != nil {
		return nil, err
	}

	entry := &cachedConsolidated{
		stats:      stats,
		orgMap:     make(map[string]OrgMetadata, len(stats.Orgs)),
		repoMap:    make(map[string]RepoStats, len(stats.Repos)),
		packageMap: make(map[string]PackageData, len(stats.Packages)),
	}

	for _, org := range stats.Orgs {
		entry.orgMap[org.Login] = org
	}
	for _, repo := range stats.Repos {
		key := fmt.Sprintf("%s/%s", repo.Org, repo.Name)
		entry.repoMap[key] = repo
	}
	for _, pkg := range stats.Packages {
		key := fmt.Sprintf("%s/%s/%s/%s", pkg.Org, pkg.Repo, pkg.PackageName, pkg.PackageType)
		entry.packageMap[key] = pkg
	}

	consolidatedCache[filePath] = entry
	return entry, nil
}

// readConsolidatedJSONFromDisk reads consolidated stats from disk without touching the cache.
func readConsolidatedJSONFromDisk(filePath string) (ConsolidatedStats, error) {
	result := ConsolidatedStats{
		Orgs:     []OrgMetadata{},
		Repos:    []RepoStats{},
		Packages: []PackageData{},
	}

	if !fileExists(filePath) {
		return result, nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return result, fmt.Errorf("failed to read JSON file %s: %w", filePath, err)
	}

	if len(data) == 0 {
		return result, nil
	}

	if err := json.Unmarshal(data, &result); err != nil {
		return result, fmt.Errorf("failed to parse JSON file %s: %w", filePath, err)
	}

	return result, nil
}

// ReadExistingJSON reads an existing JSON file and returns a map of already processed repositories.
// The map key is "org/repo" and the value is the RepoStats struct.
// Returns an empty map if the file doesn't exist or can't be read.
// Supports both consolidated and legacy formats.
func ReadExistingJSON(filePath string) (map[string]RepoStats, error) {
	result := make(map[string]RepoStats)

	// Check if file exists
	if !fileExists(filePath) {
		return result, nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read JSON file %s: %w", filePath, err)
	}

	// Parse JSON if file is not empty
	if len(data) == 0 {
		return result, nil
	}

	// Try to parse as consolidated format first
	var consolidated ConsolidatedStats
	if err := json.Unmarshal(data, &consolidated); err == nil && len(consolidated.Repos) > 0 {
		// Build map from consolidated format
		for _, stat := range consolidated.Repos {
			// Use the repo name from the struct (Name field maps to "repo" JSON field)
			if stat.Name == "" {
				// Skip repos without names (shouldn't happen but be safe)
				continue
			}
			key := fmt.Sprintf("%s/%s", stat.Org, stat.Name)
			result[key] = stat
		}
		return result, nil
	}

	// Fall back to legacy format (simple array)
	var stats []RepoStats
	if err := json.Unmarshal(data, &stats); err != nil {
		return nil, fmt.Errorf("failed to parse JSON file %s: %w", filePath, err)
	}

	// Build map
	for _, stat := range stats {
		key := fmt.Sprintf("%s/%s", stat.Org, stat.Name)
		result[key] = stat
	}

	return result, nil
}

// ReadPackageDataJSON reads package data from a JSON file and returns both package counts and version counts.
// The map key is the repository name and the value contains both counts.
// Returns an empty map if the file doesn't exist or can't be read.
// Supports both consolidated format (reads from main file) and legacy separate package file.
func ReadPackageDataJSON(filePath string) (map[string]PackageCounts, error) {
	repoPackages := make(map[string]map[string]PackageData)

	// Check if file exists
	if !fileExists(filePath) {
		return make(map[string]PackageCounts), nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read package JSON file: %w", err)
	}

	// Parse JSON if file is not empty
	if len(data) == 0 {
		return make(map[string]PackageCounts), nil
	}

	// Try to parse as consolidated format first
	var consolidated ConsolidatedStats
	var packages []PackageData

	if err := json.Unmarshal(data, &consolidated); err == nil && len(consolidated.Packages) > 0 {
		// Use packages from consolidated format
		packages = consolidated.Packages
	} else {
		// Fall back to legacy format (simple array)
		if err := json.Unmarshal(data, &packages); err != nil {
			return nil, fmt.Errorf("failed to parse package JSON file: %w", err)
		}
	}

	// Build repository packages map
	for _, pkg := range packages {
		if _, ok := repoPackages[pkg.Repo]; !ok {
			repoPackages[pkg.Repo] = make(map[string]PackageData)
		}
		repoPackages[pkg.Repo][pkg.PackageName] = pkg
	}

	// Convert to counts
	counts := make(map[string]PackageCounts)
	for repo, pkgs := range repoPackages {
		packageCount := len(pkgs)
		versionCount := 0

		// Sum up version counts for all packages in this repository
		for _, pkg := range pkgs {
			if pkg.VersionCount != nil {
				versionCount += *pkg.VersionCount
			}
		}

		counts[repo] = PackageCounts{
			PackageCount:    packageCount,
			PackageVersions: versionCount,
		}
	}

	return counts, nil
}
