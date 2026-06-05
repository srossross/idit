package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/srossross/idit/src/lsputil"
	"github.com/srossross/idit/src/treesitter"
)

// A target is either an explicit position (`file.ext:line:col`) or a symbol path
// (`file.ext#scope.name`) resolved by tree-sitter. resolveLocation turns either
// form into an exact position; resolveRange does the same for the range commands.

// locatorsHelp documents the target metavars; it's appended to the root help.
const locatorsHelp = `Locators
  <location>   file.ext:line:col      a position (col defaults to 1)
               file.ext#scope.name    a tree-sitter symbol path, e.g. main.x
  <range>      file.ext:l:c-l:c       an explicit selection
               file.ext#scope.name    the symbol's declaration span
  A symbol path matching several declarations lists them and exits 3;
  one matching none exits 2.`

// locationNote / rangeNote are one-line Long blurbs so each command's own help is
// self-contained without bloating its usage line.
const locationNote = "<location> is file.ext:line:col or file.ext#scope.name " +
	"(a tree-sitter symbol path; run `idit help` for details)."

const rangeNote = "<range> is file.ext:l:c-l:c or file.ext#scope.name " +
	"(the symbol's declaration span; run `idit help` for details)."

// locateCandidate is one resolved/ambiguous location and the --json shape. span
// (the enclosing declaration) is carried for range commands; it isn't serialized.
type locateCandidate struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Kind string `json:"kind,omitempty"`
	Text string `json:"text,omitempty"`
	span lsputil.Span
}

// notFoundError means a symbol path resolved to nothing.
type notFoundError struct{ arg, segment string }

func (e *notFoundError) Error() string {
	return fmt.Sprintf("%s: %q not found", e.arg, e.segment)
}

// ambiguousError means a symbol path matched more than one declaration; it carries
// the candidates so the caller can list them.
type ambiguousError struct {
	arg     string
	segment string
	within  string
	cands   []locateCandidate
}

func (e *ambiguousError) Error() string {
	return fmt.Sprintf("%s is ambiguous — %q within %s", e.arg, e.segment, e.within)
}

// symbolResolution is the raw result of resolving a file#path arg.
type symbolResolution struct {
	arg, filePart string
	segments      []string
	outcome       treesitter.LocateOutcome
	cands         []locateCandidate
}

func (r symbolResolution) notFound() *notFoundError {
	return &notFoundError{arg: r.arg, segment: r.segments[r.outcome.Segment]}
}

func (r symbolResolution) ambiguous() *ambiguousError {
	within := r.filePart
	if r.outcome.Segment > 0 {
		within += "#" + strings.Join(r.segments[:r.outcome.Segment], ".")
	}
	return &ambiguousError{arg: r.arg, segment: r.segments[r.outcome.Segment], within: within, cands: r.cands}
}

// resolveSymbol parses a file#path arg and runs tree-sitter Locate. It returns a
// plain error for a malformed arg, an extension with no grammar, or an unreadable
// file; the not-found / ambiguous cases are reported via the returned resolution.
func resolveSymbol(arg string) (symbolResolution, error) {
	hash := strings.IndexByte(arg, '#')
	if hash < 1 || hash == len(arg)-1 {
		return symbolResolution{}, fmt.Errorf("expected <file>#<symbol.path>, e.g. foo.go#main.x")
	}
	filePart, pathPart := arg[:hash], arg[hash+1:]
	segments := strings.Split(pathPart, ".")

	file := resolveCwd(filePart)
	ext := strings.ToLower(filepath.Ext(file))
	g := treesitter.ForExt(ext)
	if g == nil {
		return symbolResolution{}, fmt.Errorf("no tree-sitter grammar for %s files", extOrNone(ext))
	}
	//nolint:gosec // file is the path the user asked us to resolve within
	src, err := os.ReadFile(file)
	if err != nil {
		return symbolResolution{}, fmt.Errorf("cannot read %s: %v", file, err)
	}

	outcome := g.Locate(src, segments)
	cache := lineCache{}
	cands := make([]locateCandidate, len(outcome.Matches))
	for i, m := range outcome.Matches {
		cands[i] = locateCandidate{
			File: file, Line: m.Line, Col: m.Col, Kind: m.Kind,
			Text: cache.lineAt(file, m.Line), span: m.Range,
		}
	}
	return symbolResolution{arg: arg, filePart: filePart, segments: segments, outcome: outcome, cands: cands}, nil
}

// resolveLocation turns a target — `file.ext:line:col` or `file.ext#symbol.path` —
// into an exact position. err is non-nil when the file is missing, the symbol path
// is not found (*notFoundError), or ambiguous (*ambiguousError).
func resolveLocation(arg string) (Locator, error) {
	if !strings.ContainsRune(arg, '#') {
		loc, err := parseLocator(arg)
		if err != nil {
			return Locator{}, err
		}
		loc.File = resolveCwd(loc.File)
		if !regularFile(loc.File) {
			return Locator{}, fmt.Errorf("no such file: %s", loc.File)
		}
		return loc, nil
	}
	res, err := resolveSymbol(arg)
	if err != nil {
		return Locator{}, err
	}
	switch len(res.cands) {
	case 1:
		c := res.cands[0]
		return Locator{File: c.File, Line: c.Line, Col: c.Col}, nil
	case 0:
		return Locator{}, res.notFound()
	default:
		return Locator{}, res.ambiguous()
	}
}

// resolveRange turns a target — `file.ext:l:c-l:c` or `file.ext#symbol.path` (the
// symbol's full span) — into a range, with the same error contract as
// resolveLocation.
func resolveRange(arg string) (RangeLocator, error) {
	if !strings.ContainsRune(arg, '#') {
		return parseRange(arg)
	}
	res, err := resolveSymbol(arg)
	if err != nil {
		return RangeLocator{}, err
	}
	switch len(res.cands) {
	case 1:
		c := res.cands[0]
		return RangeLocator{
			File: c.File, StartLine: c.span.StartLine, StartCol: c.span.StartCol,
			EndLine: c.span.EndLine, EndCol: c.span.EndCol,
		}, nil
	case 0:
		return RangeLocator{}, res.notFound()
	default:
		return RangeLocator{}, res.ambiguous()
	}
}

// mustResolve resolves a point target or exits: ambiguous → list candidates and
// exit 3; not found → exit 2; any other error → exit 1.
func mustResolve(arg string) Locator {
	loc, err := resolveLocation(arg)
	if err != nil {
		exitResolveErr(err)
	}
	return loc
}

// mustResolveRange is mustResolve for the range commands.
func mustResolveRange(arg string) RangeLocator {
	r, err := resolveRange(arg)
	if err != nil {
		exitResolveErr(err)
	}
	return r
}

// exitResolveErr prints the right diagnostic and exits with the matching code.
func exitResolveErr(err error) {
	var amb *ambiguousError
	if errors.As(err, &amb) {
		fmt.Fprintf(os.Stderr, "idit: %s, please choose:\n", amb.Error())
		printCandidates(amb.cands)
		os.Exit(3)
	}
	var nf *notFoundError
	if errors.As(err, &nf) {
		fmt.Fprintf(os.Stderr, "idit: %v\n", nf)
		os.Exit(2)
	}
	fail("%v", err)
}

// printCandidates writes ambiguous candidates to stdout, grep-style.
func printCandidates(cands []locateCandidate) {
	for _, c := range cands {
		fmt.Printf("%s:%d:%d:%s\n", c.File, c.Line, c.Col, c.Text)
	}
}

func regularFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// extOrNone renders an extension for an error message, or "(none)" when empty.
func extOrNone(ext string) string {
	if ext == "" {
		return "(none)"
	}
	return ext
}
