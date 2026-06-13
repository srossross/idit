package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/srossross/idit/src/fsscan"
	"github.com/srossross/idit/src/treesitter"
)

// findFileLimit caps how many files a search reads. Parsing/grepping is local and
// cheap, so this is generous; ripgrep already narrows the matching set.
const findFileLimit = 5000

// parseKinds resolves a comma-separated kind list (with aliases) to canonical
// kinds, preserving order and dropping duplicates. An empty list means "no kind
// filter" (plain grep).
func parseKinds(list string) ([]string, error) {
	if strings.TrimSpace(list) == "" {
		return nil, nil
	}
	seen := map[string]bool{}
	var out []string
	for raw := range strings.SplitSeq(list, ",") {
		name := strings.TrimSpace(strings.ToLower(raw))
		if name == "" {
			continue
		}
		canon, ok := treesitter.CanonicalKind(name)
		if !ok {
			return nil, fmt.Errorf("unknown --kind %q", raw)
		}
		if !seen[canon] {
			seen[canon] = true
			out = append(out, canon)
		}
	}
	return out, nil
}

// parseLangs resolves a comma-separated language list (with aliases) to the set
// of file extensions to keep. An empty list returns a nil set, meaning "no
// language filter".
func parseLangs(list string) (map[string]bool, error) {
	if strings.TrimSpace(list) == "" {
		return nil, nil
	}
	exts := map[string]bool{}
	for raw := range strings.SplitSeq(list, ",") {
		name := strings.TrimSpace(strings.ToLower(raw))
		if name == "" {
			continue
		}
		langExts, ok := treesitter.CanonicalLang(name)
		if !ok {
			return nil, fmt.Errorf("unknown --lang %q (known: %s)", raw, strings.Join(treesitter.LangNames(), ", "))
		}
		for _, e := range langExts {
			exts[e] = true
		}
	}
	return exts, nil
}

// discoverFiles returns the files under paths to search. For a normal search it
// uses ripgrep to keep only files whose contents match (a prefilter; matching is
// re-checked per file). For --kind it keeps only files with a bundled grammar.
// When all is set (invert), it returns every candidate file regardless of match.
//
// skipped holds the distinct extensions of matching files dropped for lacking a
// grammar (only under --kind), so the caller can explain an empty result.
//
// langExts, when non-empty, restricts results to files whose extension is in the
// set (the --lang filter); a nil/empty set imposes no language restriction.
func discoverFiles(paths []string, pattern string, re *regexp.Regexp, requireGrammar, all, noIgnore bool, langExts map[string]bool) (files, skipped []string) {
	hasGrammar := func(name string) bool {
		return treesitter.HasExt(strings.ToLower(filepath.Ext(name)))
	}
	inLang := func(name string) bool {
		return len(langExts) == 0 || langExts[strings.ToLower(filepath.Ext(name))]
	}
	hasExt := func(name string) bool {
		return inLang(name) && (!requireGrammar || hasGrammar(name))
	}
	seen := map[string]bool{}
	skip := map[string]bool{}
	add := func(f string) {
		if seen[f] {
			return
		}
		if hasExt(f) {
			seen[f] = true
			files = append(files, f)
		} else if requireGrammar && inLang(f) && !hasGrammar(f) {
			// Only files dropped specifically for lacking a grammar are reported;
			// a --lang mismatch is an intentional filter, not a skip to explain.
			skip[strings.ToLower(filepath.Ext(f))] = true
		}
	}
	for _, p := range paths {
		root := resolveCwd(p)
		// A file path is searched directly; per-file matching decides the rest.
		// Only directories go through ripgrep / the walk.
		if info, err := os.Stat(root); err == nil && !info.IsDir() {
			add(root)
			continue
		}
		var found []string
		switch {
		case all:
			found = fsscan.ScanFiles(root, hasExt, func([]byte) bool { return true }, findFileLimit)
		default:
			found = fsscan.RipgrepFilesRegex(root, pattern, noIgnore)
			if found == nil {
				found = fsscan.ScanFiles(root, hasExt, re.Match, findFileLimit)
			}
		}
		for _, f := range found {
			add(f)
		}
	}
	sort.Strings(files)
	for ext := range skip {
		skipped = append(skipped, ext)
	}
	sort.Strings(skipped)
	return files, skipped
}

// ignoredMatchCount reports how many additional files would be searched if ignore
// rules were lifted — used only on an empty result to hint at .gitignore hiding
// matches. Returns 0 when --no-ignore is already set or nothing extra matches.
func ignoredMatchCount(paths []string, pattern string, re *regexp.Regexp, requireGrammar, noIgnore bool, found int, langExts map[string]bool) int {
	if noIgnore {
		return 0
	}
	all, _ := discoverFiles(paths, pattern, re, requireGrammar, false, true, langExts)
	return len(all) - found
}
