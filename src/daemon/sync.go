package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/srossross/clidit/src/ipc"
	"github.com/srossross/clidit/src/lsputil"
)

// --- locate / sync ---

type located struct {
	uri      string
	position lsputil.Position
}

func positionParams(at located) map[string]any {
	return map[string]any{
		"textDocument": map[string]any{"uri": at.uri},
		"position":     at.position,
	}
}

// locate is the shared preamble for positional ops: validate the target, ensure
// the file is open, and wait out any project load a cold open triggered. Returns
// the URI + LSP position to query, or an error response.
func locate(ctx *Context, req ipc.Request) (located, *ipc.Response) {
	file := req.File
	if file == "" {
		r := ipc.Errorf("missing file")
		return located{}, &r
	}
	line := req.Line
	col := req.Col
	if col == 0 {
		col = 1
	}
	if line < 1 {
		r := ipc.Errorf("invalid line: %d", req.Line)
		return located{}, &r
	}
	if col < 1 {
		r := ipc.Errorf("invalid col: %d", req.Col)
		return located{}, &r
	}

	doc := syncDoc(ctx, file)
	if doc == nil {
		r := ipc.Errorf("cannot read file: %s", file)
		return located{}, &r
	}
	// A cold open may kick off project loading; wait for it so the query doesn't
	// race ahead and return a partial result.
	if doc.fresh {
		ctx.Progress.Settle(progressMax)
	}
	return located{uri: doc.uri, position: lsputil.ToLSPPosition(line, col)}, nil
}

type syncResult struct {
	uri     string
	fresh   bool // this call did the initial didOpen
	changed bool // the server's view of the file changed (open or edit)
}

// syncDoc syncs the file's current on-disk content into the server, then returns
// its URI. Sends didOpen the first time and didChange when the file changed since
// we last synced it. Returns nil if the file can't be read.
func syncDoc(ctx *Context, absPath string) *syncResult {
	uri := lsputil.FileToURI(absPath)
	//nolint:gosec // absPath is a workspace source file the request targets
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil
	}
	text := string(data)
	prev, ok := ctx.getOpen(uri)

	if !ok {
		ctx.Lsp.Notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{
				"uri": uri, "languageId": ctx.Server.LanguageID, "version": 1, "text": text,
			},
		})
		ctx.setOpen(uri, openDoc{version: 1, text: text})
		return &syncResult{uri: uri, fresh: true, changed: true}
	}

	if prev.text != text {
		version := prev.version + 1
		ctx.Lsp.Notify("textDocument/didChange", map[string]any{
			"textDocument":   map[string]any{"uri": uri, "version": version},
			"contentChanges": []map[string]any{{"text": text}},
		})
		ctx.setOpen(uri, openDoc{version: version, text: text})
		return &syncResult{uri: uri, fresh: false, changed: true}
	}

	return &syncResult{uri: uri, fresh: false, changed: false}
}

// --- project loading for symbol search ---

var ignoredDirs = map[string]struct{}{
	"node_modules": {}, ".git": {}, ".idit": {}, "dist": {}, "build": {}, "out": {}, "coverage": {},
}

// Cap on how many matching files we open to load projects for a symbol search.
const openLimit = 25

// loadProjectsMentioning opens the source files that mention query so the
// project(s) containing the symbol get loaded before workspace/symbol runs.
func loadProjectsMentioning(ctx *Context, query string) {
	hasExt := extMatcher(ctx)
	files := ripgrepFiles(ctx.Root, query)
	if files == nil {
		files = scanFiles(ctx.Root, hasExt, func(data []byte) bool {
			return strings.Contains(string(data), query)
		}, openLimit)
	}
	var matched []string
	for _, f := range files {
		if hasExt(f) {
			matched = append(matched, f)
			if len(matched) >= openLimit {
				break
			}
		}
	}

	fresh := false
	for _, file := range matched {
		if doc := syncDoc(ctx, file); doc != nil && doc.fresh {
			fresh = true
		}
	}
	if fresh {
		ctx.Progress.Settle(progressMax)
	}
}

// candidateFilesRegex returns up to limit extension-matching files whose contents
// match re, via ripgrep in regex mode (falling back to a bounded scan when
// ripgrep isn't available). ripgrep's regex engine is close enough to RE2 to use
// as a prefilter; the caller re-checks each span with re itself, so any
// over-inclusion here is harmless.
func candidateFilesRegex(ctx *Context, pattern string, re *regexp.Regexp, limit int) []string {
	hasExt := extMatcher(ctx)
	files := ripgrepFilesRegex(ctx.Root, pattern)
	if files == nil {
		files = scanFiles(ctx.Root, hasExt, re.Match, limit)
	}
	var matched []string
	for _, f := range files {
		if hasExt(f) {
			matched = append(matched, f)
			if len(matched) >= limit {
				break
			}
		}
	}
	return matched
}

// extMatcher returns a predicate reporting whether a path's extension is one the
// server handles.
func extMatcher(ctx *Context) func(string) bool {
	exts := map[string]struct{}{}
	for _, e := range ctx.Server.Extensions {
		exts[strings.ToLower(e)] = struct{}{}
	}
	return func(path string) bool {
		dot := strings.LastIndexByte(path, '.')
		if dot == -1 {
			return false
		}
		_, ok := exts[strings.ToLower(path[dot:])]
		return ok
	}
}

// ripgrepFiles returns files containing query (literal) via ripgrep, or nil if
// ripgrep isn't available or errored.
func ripgrepFiles(root, query string) []string {
	return runRipgrep("--files-with-matches", "--fixed-strings", "--", query, root)
}

// ripgrepFilesRegex returns files whose contents match the regex pattern via
// ripgrep, or nil if ripgrep isn't available or errored.
func ripgrepFilesRegex(root, pattern string) []string {
	return runRipgrep("--files-with-matches", "--", pattern, root)
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

// scanFiles is the no-ripgrep fallback: a bounded directory walk returning up to
// limit extension-matching files whose contents satisfy match.
func scanFiles(root string, hasExt func(string) bool, match func([]byte) bool, limit int) []string {
	var matches []string
	stack := []string{root}
	budget := 4000
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
				if _, ignored := ignoredDirs[entry.Name()]; !ignored && !strings.HasPrefix(entry.Name(), ".") {
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

// --- misc ---

// summarizeCapabilities reduces the verbose LSP capability object to the
// providers we care about.
func summarizeCapabilities(caps map[string]any) map[string]bool {
	has := func(k string) bool {
		v, ok := caps[k]
		return ok && v != nil && v != false
	}
	return map[string]bool{
		"definition": has("definitionProvider"),
		"references": has("referencesProvider"),
		"hover":      has("hoverProvider"),
		"rename":     has("renameProvider"),
		"codeAction": has("codeActionProvider"),
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// errText turns anything recovered or thrown into a readable message (port of
// errors.ts), unwrapping opError.
func errText(v any) string {
	switch e := v.(type) {
	case opError:
		return e.err.Error()
	case error:
		return e.Error()
	case string:
		return e
	default:
		return fmt.Sprintf("%v", v)
	}
}
