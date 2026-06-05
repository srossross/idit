package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/srossross/clidit/src/fsscan"
	"github.com/srossross/clidit/src/lsputil"
	"github.com/srossross/clidit/src/treesitter"
)

// findFileLimit caps how many files a search reads. Parsing/grepping is local and
// cheap, so this is generous; ripgrep already narrows the matching set.
const findFileLimit = 5000

func newFindCmd() *cobra.Command {
	var kindList string
	var ignoreCase, invert, asJSON, noIgnore bool
	var context int
	cmd := &cobra.Command{
		Use:   "find <regex> [path...]",
		Short: "grep a regex across files, optionally restricted to a --kind",
		Long: "find greps a RE2 regex across files like grep. With --kind it restricts\n" +
			"matches to spans the parser classifies — string literals, comments, or\n" +
			"symbol-definition names (function, method, class, interface, type,\n" +
			"variable, const). Kinds are comma-separated and accept aliases (func, var,\n" +
			"str, …). Tree-sitter handles --kind; no language server is involved.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			pattern := args[0]
			paths := args[1:]
			if len(paths) == 0 {
				paths = []string{"."}
			}
			kinds, err := parseKinds(kindList)
			if err != nil {
				fail("%v", err)
			}

			p := pattern
			if ignoreCase {
				p = "(?i)" + pattern
			}
			re, err := regexp.Compile(p)
			if err != nil {
				fail("invalid regex: %v", err)
			}

			useKinds := len(kinds) > 0
			// Invert lists non-matching lines, so it must see every file, not just
			// the ones ripgrep says contain the pattern.
			files, skipped := discoverFiles(paths, p, re, useKinds, invert, noIgnore)
			// Explain why matching files were ignored: --kind only searches
			// languages with a bundled grammar.
			if useKinds && len(skipped) > 0 {
				fmt.Fprintf(os.Stderr, "idit: --kind skipped files with no grammar (%s); supported: %s\n",
					strings.Join(skipped, " "), strings.Join(treesitter.Extensions(), " "))
			}

			matched := map[string]map[int]int{} // file -> line -> first match col
			fileLines := map[string][]string{}
			var sites []lsputil.Site
			for _, f := range files {
				//nolint:gosec // f is a path the user asked us to search
				src, err := os.ReadFile(f)
				if err != nil {
					continue
				}
				var found []lsputil.Site
				if useKinds {
					if g := treesitter.ForExt(strings.ToLower(filepath.Ext(f))); g != nil {
						found = g.Search(f, src, kinds, re)
					}
				} else {
					found = lineMatches(f, src, re)
				}
				if len(found) == 0 && !invert {
					continue
				}
				fileLines[f] = strings.Split(string(src), "\n")
				sites = append(sites, found...)
				lineCols := matched[f]
				if lineCols == nil {
					lineCols = map[int]int{}
					matched[f] = lineCols
				}
				for _, s := range found {
					if c, ok := lineCols[s.Line]; !ok || s.Col < c {
						lineCols[s.Line] = s.Col
					}
				}
			}

			if invert {
				return renderInvert(files, fileLines, matched, asJSON)
			}
			if asJSON {
				sort.SliceStable(sites, func(i, j int) bool { return siteLess(sites[i], sites[j]) })
				printJSON(orEmptySites(sites))
				return nil
			}
			if len(sites) == 0 {
				fmt.Fprintln(os.Stderr, "no matches found")
				if n := ignoredMatchCount(paths, p, re, useKinds, noIgnore, len(files)); n > 0 {
					fmt.Fprintf(os.Stderr, "idit: %d more file(s) match but are ignored (.gitignore/.ignore); use --no-ignore to include them\n", n)
				}
				os.Exit(2)
			}
			renderMatches(matched, fileLines, context)
			return nil
		},
	}
	cmd.Flags().StringVarP(&kindList, "kind", "k", "", "restrict to comma-separated kinds (string,comment,function,method,class,interface,type,variable,const; aliases ok)")
	cmd.Flags().BoolVarP(&ignoreCase, "ignore-case", "i", false, "case-insensitive match")
	cmd.Flags().IntVarP(&context, "context", "C", 0, "print N lines of context around each match")
	cmd.Flags().BoolVarP(&invert, "invert-match", "v", false, "select lines NOT matching")
	cmd.Flags().BoolVarP(&noIgnore, "no-ignore", "u", false, "also search files excluded by .gitignore/.ignore")
	cmd.Flags().BoolVar(&asJSON, "json", false, "structured output")
	return cmd
}

// ignoredMatchCount reports how many additional files would be searched if ignore
// rules were lifted — used only on an empty result to hint at .gitignore hiding
// matches. Returns 0 when --no-ignore is already set or nothing extra matches.
func ignoredMatchCount(paths []string, pattern string, re *regexp.Regexp, requireGrammar, noIgnore bool, found int) int {
	if noIgnore {
		return 0
	}
	all, _ := discoverFiles(paths, pattern, re, requireGrammar, false, true)
	return len(all) - found
}

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

// lineMatches returns a Site for every regex match across src, line by line — the
// plain-grep path used when no --kind is given. Columns are 1-based UTF-16.
func lineMatches(file string, src []byte, re *regexp.Regexp) []lsputil.Site {
	var sites []lsputil.Site
	lineNo := 1
	start := 0
	emit := func(line []byte) {
		for _, loc := range re.FindAllIndex(line, -1) {
			col := lsputil.UTF16Len(string(line[:loc[0]])) + 1
			endCol := lsputil.UTF16Len(string(line[:loc[1]])) + 1
			sites = append(sites, lsputil.Site{
				File: file, Line: lineNo, Col: col,
				Range: lsputil.Span{StartLine: lineNo, StartCol: col, EndLine: lineNo, EndCol: endCol},
			})
		}
	}
	for i := 0; i <= len(src); i++ {
		if i == len(src) || src[i] == '\n' {
			emit(src[start:i])
			lineNo++
			start = i + 1
		}
	}
	return sites
}

// discoverFiles returns the files under paths to search. For a normal search it
// uses ripgrep to keep only files whose contents match (a prefilter; matching is
// re-checked per file). For --kind it keeps only files with a bundled grammar.
// When all is set (invert), it returns every candidate file regardless of match.
//
// skipped holds the distinct extensions of matching files dropped for lacking a
// grammar (only under --kind), so the caller can explain an empty result.
func discoverFiles(paths []string, pattern string, re *regexp.Regexp, requireGrammar, all, noIgnore bool) (files, skipped []string) {
	hasGrammar := func(name string) bool {
		return treesitter.HasExt(strings.ToLower(filepath.Ext(name)))
	}
	hasExt := func(name string) bool { return !requireGrammar || hasGrammar(name) }
	seen := map[string]bool{}
	skip := map[string]bool{}
	add := func(f string) {
		if seen[f] {
			return
		}
		if hasExt(f) {
			seen[f] = true
			files = append(files, f)
		} else if requireGrammar {
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

// window is an inclusive 1-based line range to print as one group.
type window struct{ lo, hi int }

// mergeWindows expands each matched line by context lines and merges overlapping
// or adjacent ranges into ordered groups.
func mergeWindows(lines []int, context, maxLine int) []window {
	var ws []window
	for _, l := range lines {
		lo := max(1, l-context)
		hi := min(maxLine, l+context)
		if n := len(ws); n > 0 && lo <= ws[n-1].hi+1 {
			ws[n-1].hi = max(ws[n-1].hi, hi)
		} else {
			ws = append(ws, window{lo, hi})
		}
	}
	return ws
}

// renderMatches prints matches grep-style: `file:line:col:text` for match lines,
// `file-line-text` for context lines, with `--` between non-adjacent groups when
// context is requested.
func renderMatches(matched map[string]map[int]int, fileLines map[string][]string, context int) {
	files := make([]string, 0, len(matched))
	for f := range matched {
		files = append(files, f)
	}
	sort.Strings(files)

	first := true
	for _, file := range files {
		lineCols := matched[file]
		matchLines := make([]int, 0, len(lineCols))
		for n := range lineCols {
			matchLines = append(matchLines, n)
		}
		sort.Ints(matchLines)
		lines := fileLines[file]
		for _, w := range mergeWindows(matchLines, context, len(lines)) {
			if context > 0 && !first {
				fmt.Println("--")
			}
			first = false
			for n := w.lo; n <= w.hi; n++ {
				if col, ok := lineCols[n]; ok {
					fmt.Printf("%s:%d:%d:%s\n", file, n, col, lineTextAt(lines, n))
				} else {
					fmt.Printf("%s-%d-%s\n", file, n, lineTextAt(lines, n))
				}
			}
		}
	}
}

// renderInvert prints, for every searched file, the lines that hold no qualifying
// match — grep's -v, scoped by --kind when given.
func renderInvert(files []string, fileLines map[string][]string, matched map[string]map[int]int, asJSON bool) error {
	var sites []lsputil.Site
	for _, file := range files {
		lines, ok := fileLines[file]
		if !ok {
			continue
		}
		lineCols := matched[file]
		// A trailing newline yields a final empty element from Split; don't emit it.
		limit := len(lines)
		if limit > 0 && lines[limit-1] == "" {
			limit--
		}
		for n := 1; n <= limit; n++ {
			if _, isMatch := lineCols[n]; isMatch {
				continue
			}
			sites = append(sites, lsputil.Site{File: file, Line: n, Col: 1})
		}
	}
	if asJSON {
		printJSON(orEmptySites(sites))
		return nil
	}
	if len(sites) == 0 {
		fmt.Fprintln(os.Stderr, "no matches found")
		os.Exit(2)
	}
	for _, s := range sites {
		fmt.Printf("%s:%d:%s\n", s.File, s.Line, lineTextAt(fileLines[s.File], s.Line))
	}
	return nil
}

// lineTextAt returns the trimmed text of a 1-based line, or "" if out of range.
func lineTextAt(lines []string, n int) string {
	if n-1 < 0 || n-1 >= len(lines) {
		return ""
	}
	return strings.TrimRight(lines[n-1], " \t\r")
}
