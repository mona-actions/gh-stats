// Package stats implements concurrent processing of GitHub repository statistics.
//
// This file (validation.go) contains input validation and organization list loading.
// It validates organization names against GitHub's naming rules and safely loads
// organization lists from files with security protections.
//
// Key features:
//   - GitHub organization name validation
//   - Safe file loading with path traversal protection
//   - Multi-line format support (Unix/Windows)
//   - Empty line and whitespace handling
package stats

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// orgNamePattern validates GitHub organization names according to GitHub's rules:
// - Must start and end with alphanumeric character
// - Can contain alphanumeric characters and hyphens in the middle
// - Maximum 39 characters (enforced separately)
var orgNamePattern = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$`)

// validateOrgName checks if the organization name is valid according to GitHub's rules.
// GitHub organization names:
//   - Must be 1-39 characters long
//   - Can only contain alphanumeric characters and hyphens
//   - Cannot start or end with a hyphen
func validateOrgName(org string) error {
	if org == "" {
		return fmt.Errorf("organization name cannot be empty")
	}
	if len(org) > 39 {
		return fmt.Errorf("organization name too long (max 39 characters): %s", org)
	}
	if !orgNamePattern.MatchString(org) {
		return fmt.Errorf("invalid organization name format: %s (must contain only alphanumeric characters and hyphens, cannot start/end with hyphen)", org)
	}
	return nil
}

// loadOrgs loads and validates organization names from either a single value or file.
//
// This function handles input from command-line flags, supporting both single organization
// analysis and batch processing from a file. It performs validation, deduplication, and
// path traversal protection.
//
// File format:
//   - One organization name per line
//   - Empty lines are ignored
//   - Lines starting with # are treated as comments
//   - Leading/trailing whitespace is automatically trimmed
//
// Security:
//   - Path traversal protection via filepath.Clean and validation
//   - Organization names validated against GitHub's rules
//
// Returns:
//   - Deduplicated list of valid organization names
//   - Error if inputs are invalid, file cannot be read, or no valid orgs found
//
// validateFilePath validates that the file path is safe to read.
// Returns the absolute path if valid, or an error if the path is unsafe.
func validateFilePath(file string) (string, error) {
	// Validate file path - resolve to absolute path and check it's safe
	cleanPath := filepath.Clean(file)
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return "", fmt.Errorf("invalid file path %s: %w", file, err)
	}

	// Get current working directory to validate the resolved path
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get working directory: %w", err)
	}

	// Check if the resolved path is within the current working directory
	relPath, err := filepath.Rel(cwd, absPath)
	if err != nil {
		return "", fmt.Errorf("invalid file path %s: %w", file, err)
	}

	// If the relative path starts with "..", it's trying to access a parent directory
	if strings.HasPrefix(relPath, "..") {
		return "", fmt.Errorf("file path must be within current directory: %s resolves to %s", file, absPath)
	}

	// Security: Block access to sensitive directories within the working directory
	dangerousPaths := []string{
		".git", ".ssh", ".aws", ".kube", ".docker", ".gnupg", ".config",
	}

	// Normalize path for comparison
	normalizedPath := filepath.ToSlash(relPath)
	for _, danger := range dangerousPaths {
		if normalizedPath == danger || strings.HasPrefix(normalizedPath, danger+"/") {
			return "", fmt.Errorf("access denied: cannot read from %s directory for security reasons", danger)
		}
	}

	// Validate file extension
	ext := strings.ToLower(filepath.Ext(absPath))
	allowedExtensions := map[string]bool{
		".txt": true, ".list": true, ".csv": true, "": true,
	}
	if !allowedExtensions[ext] {
		return "", fmt.Errorf("invalid file extension %s: only .txt, .list, .csv, or no extension allowed", ext)
	}

	return absPath, nil
}

// parseOrgFile reads and parses an organization list file.
func parseOrgFile(absPath string) ([]string, error) {
	content, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("reading organization file %s: %w", absPath, err)
	}

	lines := splitLines(string(content))
	var orgs []string
	seen := make(map[string]bool)
	orgs = make([]string, 0, len(lines)/2)

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if err := validateOrgName(line); err != nil {
			return nil, fmt.Errorf("line %d: invalid organization name '%s': %w", i+1, line, err)
		}

		if seen[line] {
			continue
		}
		seen[line] = true
		orgs = append(orgs, line)
	}

	if len(orgs) == 0 {
		return nil, fmt.Errorf("no valid organizations found in %s", absPath)
	}

	return orgs, nil
}

// loadOrgs loads organization names from either a single org name or a file.
func loadOrgs(single string, file string) ([]string, error) {
	if single != "" {
		return []string{single}, nil
	}

	if file == "" {
		return nil, fmt.Errorf("must specify either --org or --file")
	}

	absPath, err := validateFilePath(file)
	if err != nil {
		return nil, err
	}

	return parseOrgFile(absPath)
}

// splitLines splits input text into lines, handling both Unix and Windows line endings.
//
// This helper function normalizes line endings and removes empty lines to simplify
// file parsing. It's designed to be forgiving of different text file formats.
//
// Behavior:
//   - Converts Windows (CRLF) to Unix (LF) line endings
//   - Removes empty lines after trimming whitespace
//   - Preserves all non-empty lines with whitespace trimmed
//
// Returns:
//   - Slice of non-empty, trimmed lines
func splitLines(input string) []string {
	// Normalize line endings and split
	normalized := strings.ReplaceAll(input, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")

	// Pre-allocate result slice with capacity to avoid reallocations
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}

	return result
}
