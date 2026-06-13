package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/srossross/idit/src/lsputil"
	"github.com/srossross/idit/src/treesitter"
)

// replaceEdit is one applied substitution, for reporting and --json output.
type replaceEdit struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

func newReplaceCmd() *cobra.Command {
	var kindList, langList string
	var ignoreCase, dryRun, asJSON, noIgnore bool
	cmd := &cobra.Command{
		Use:   "replace <regex> <replacement> [path...]",
		Short: "substitute a regex across files, optionally restricted to a --kind",
		Long: "replace substitutes a RE2 regex across files like `sed -i`, expanding\n" +
			"$1/${name} in the replacement from the match's submatches. With --kind it\n" +
			"restricts substitutions to spans the parser classifies — string literals,\n" +
			"comments, or symbol-definition names — something sed can't do safely.\n" +
			"Tree-sitter handles --kind; no language server is involved.\n\n" +
			"This is a text substitution, NOT a reference-aware rename: it does not\n" +
			"track a symbol's declarations and uses. To rename a symbol project-wide,\n" +
			"use `idit rename`. Files are written in place; use --dry-run to preview.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			pattern := args[0]
			replacement := args[1]
			paths := args[2:]
			if len(paths) == 0 {
				paths = []string{"."}
			}
			kinds, err := parseKinds(kindList)
			if err != nil {
				fail("%v", err)
			}
			langExts, err := parseLangs(langList)
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
			files, skipped := discoverFiles(paths, p, re, useKinds, false, noIgnore, langExts)
			if useKinds && len(skipped) > 0 {
				fmt.Fprintf(os.Stderr, "idit: --kind skipped files with no grammar (%s); %s\n",
					strings.Join(skipped, " "), strings.Join(treesitter.Extensions(), " "))
			}

			var edits []replaceEdit
			fileCount := 0
			for _, f := range files {
				//nolint:gosec // f is a path the user asked us to edit
				src, err := os.ReadFile(f)
				if err != nil {
					continue
				}
				var matches []treesitter.ReplaceMatch
				if useKinds {
					if g := treesitter.ForExt(strings.ToLower(filepath.Ext(f))); g != nil {
						matches = g.SearchReplace(src, kinds, re, replacement)
					}
				} else {
					matches = plainReplaceMatches(src, re, replacement)
				}
				if len(matches) == 0 {
					continue
				}

				out, applied := applyReplacements(src, matches)
				if len(applied) == 0 {
					continue
				}
				for _, m := range applied {
					edits = append(edits, replaceEdit{
						File: f, Line: m.Line, Col: m.Col,
						Old: string(src[m.StartByte:m.EndByte]), New: m.New,
					})
				}
				fileCount++
				if !dryRun {
					//nolint:gosec // preserve nothing fancy: source files are user-owned, 0644 is fine
					if err := os.WriteFile(f, out, 0o644); err != nil {
						fail("writing %s: %v", f, err)
					}
				}
			}

			sort.SliceStable(edits, func(i, j int) bool { return replaceEditLess(edits[i], edits[j]) })

			if asJSON {
				printJSON(orEmptyEdits(edits))
				return nil
			}
			if len(edits) == 0 {
				fmt.Fprintln(os.Stderr, "no matches found")
				os.Exit(2)
			}
			verb := "replaced"
			if dryRun {
				verb = "would replace"
			}
			fmt.Fprintf(os.Stderr, "%s: %d edit(s) across %d file(s)\n", verb, len(edits), fileCount)
			for _, e := range edits {
				fmt.Printf("%s:%d:%d\n", e.File, e.Line, e.Col)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&kindList, "kind", "k", "", "restrict to comma-separated kinds (string,comment,function,method,class,interface,type,variable,const; aliases ok)")
	cmd.Flags().StringVarP(&langList, "lang", "l", "", "restrict to comma-separated languages (go,js,ts,python,c,cpp,sql; aliases ok)")
	cmd.Flags().BoolVarP(&ignoreCase, "ignore-case", "i", false, "case-insensitive match")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "preview the edits without writing")
	cmd.Flags().BoolVarP(&noIgnore, "no-ignore", "u", false, "also edit files excluded by .gitignore/.ignore")
	cmd.Flags().BoolVar(&asJSON, "json", false, "structured output")
	return cmd
}

// plainReplaceMatches returns a ReplaceMatch for every regex match across the
// whole file — the plain path used when no --kind is given. Replacement text is
// expanded from the match's submatches; columns are 1-based UTF-16.
func plainReplaceMatches(src []byte, re *regexp.Regexp, repl string) []treesitter.ReplaceMatch {
	replBytes := []byte(repl)
	var out []treesitter.ReplaceMatch
	for _, loc := range re.FindAllSubmatchIndex(src, -1) {
		ms, me := loc[0], loc[1]
		line, col := byteToLineCol(src, ms)
		out = append(out, treesitter.ReplaceMatch{
			StartByte: ms, EndByte: me, Line: line, Col: col,
			New: string(re.Expand(nil, replBytes, src, loc)),
		})
	}
	return out
}

// applyReplacements splices matches into src, applying highest byte offset first
// so earlier offsets stay valid. It returns the rewritten content and the
// matches actually applied (overlapping matches are dropped, so reporting
// reflects what was written).
func applyReplacements(src []byte, matches []treesitter.ReplaceMatch) (out []byte, applied []treesitter.ReplaceMatch) {
	ordered := append([]treesitter.ReplaceMatch(nil), matches...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].StartByte > ordered[j].StartByte })

	out = src
	prevStart := len(src) + 1 // lowest start applied so far (we go high → low)
	for _, m := range ordered {
		if m.EndByte > prevStart {
			continue // overlaps an already-applied match; skip to keep splices safe
		}
		spliced := make([]byte, 0, len(out)-(m.EndByte-m.StartByte)+len(m.New))
		spliced = append(spliced, out[:m.StartByte]...)
		spliced = append(spliced, m.New...)
		spliced = append(spliced, out[m.EndByte:]...)
		out = spliced
		prevStart = m.StartByte
		applied = append(applied, m)
	}
	return out, applied
}

// byteToLineCol returns the 1-based line and 1-based UTF-16 column of a byte
// offset in src.
func byteToLineCol(src []byte, off int) (line, col int) {
	if off > len(src) {
		off = len(src)
	}
	line = 1
	lineStart := 0
	for i := 0; i < off; i++ {
		if src[i] == '\n' {
			line++
			lineStart = i + 1
		}
	}
	return line, lsputil.UTF16Len(string(src[lineStart:off])) + 1
}

func replaceEditLess(a, b replaceEdit) bool {
	if a.File != b.File {
		return a.File < b.File
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Col < b.Col
}

// orEmptyEdits coerces a nil slice to [] so --json emits an array, not null.
func orEmptyEdits(s []replaceEdit) []replaceEdit {
	if s == nil {
		return []replaceEdit{}
	}
	return s
}
