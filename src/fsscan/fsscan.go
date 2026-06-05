// Package fsscan finds workspace files matching a query, used to narrow the set
// of files an op has to open or parse. It prefers ripgrep and falls back to a
// bounded directory walk when ripgrep isn't installed.
package fsscan

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// IgnoredDirs are directory names the fallback walk never descends into.
var IgnoredDirs = map[string]struct{}{
	"node_modules": {}, ".git": {}, ".idit": {}, "dist": {}, "build": {}, "out": {}, "coverage": {},
}

// scanBudget caps how many files the fallback walk reads before giving up.
const scanBudget = 4000

// RipgrepFiles returns files containing query (literal) via ripgrep, or nil if
// ripgrep isn't available or errored.
func RipgrepFiles(root, query string) []string {
	return runRipgrep("--files-with-matches", "--fixed-strings", "--", query, root)
}

// RipgrepFilesRegex returns files whose contents match the regex pattern via
// ripgrep, or nil if ripgrep isn't available or errored. When noIgnore is set,
// ripgrep does not skip files excluded by .gitignore/.ignore.
func RipgrepFilesRegex(root, pattern string, noIgnore bool) []string {
	args := []string{"--files-with-matches"}
	if noIgnore {
		args = append(args, "--no-ignore")
	}
	args = append(args, "--", pattern, root)
	return runRipgrep(args...)
}

// runRipgrep runs `rg <args>` and returns the matched file paths, [] on "no
// matches", or nil when ripgrep is missing or errors.
func runRipgrep(args ...string) []string {
	//nolint:gosec // fixed argv; the query/pattern is a literal operand after `--`, not a shell string
	cmd := exec.Command("rg", args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return []string{} // 1 = no matches (fine)
		}
		return nil // ripgrep not installed or real error
	}
	var files []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

// ScanFiles is the no-ripgrep fallback: a bounded directory walk returning up to
// limit files that pass hasExt and whose contents satisfy match.
func ScanFiles(root string, hasExt func(string) bool, match func([]byte) bool, limit int) []string {
	var matches []string
	stack := []string{root}
	budget := scanBudget
	for len(stack) > 0 && budget > 0 && len(matches) < limit {
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			full := filepath.Join(dir, entry.Name())
			if entry.IsDir() {
				if _, ignored := IgnoredDirs[entry.Name()]; !ignored && !strings.HasPrefix(entry.Name(), ".") {
					stack = append(stack, full)
				}
			} else if entry.Type().IsRegular() && hasExt(entry.Name()) {
				budget--
				if budget <= 0 {
					break
				}
				//nolint:gosec // full is a path under the workspace root we're scanning
				if data, err := os.ReadFile(full); err == nil && match(data) {
					matches = append(matches, full)
				}
				if len(matches) >= limit {
					break
				}
			}
		}
	}
	return matches
}
