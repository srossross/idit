package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/srossross/clidit/internal/ipc"
	"github.com/srossross/clidit/internal/lsputil"
)

// Cap on how many completion items we resolve for `--detail` (one round-trip each).
const resolveLimit = 200

const progressMax = 30 * time.Second
const diagDebounce = 350 * time.Millisecond
const diagMax = 5 * time.Second

// opHandler processes one request against the daemon state.
type opHandler func(ctx *Context, req ipc.Request) ipc.Response

var ops map[string]opHandler

func init() {
	ops = map[string]opHandler{
		"ping":     opPing,
		"shutdown": opShutdown,
		"status":   opStatus,
		"def":      opDef,
		"refs":     opRefs,
		"type":     opType,
		"members":  opMembers,
		"outline":  opOutline,
		"symbol":   opSymbol,
		"callers":  opCallers,
		"check":    opCheck,
		"rename":   opRename,
		"mv":       opMv,
		"extract":  opExtract,
	}
}

// opError wraps an LSP/request failure so request() can panic and Dispatch can
// convert it back to an {ok:false,error} response — mirroring the TS handler's
// outer try/catch.
type opError struct{ err error }

// Dispatch runs the handler for req.Op, converting a missing op or any panic
// into an error response.
func Dispatch(ctx *Context, req ipc.Request) (resp ipc.Response) {
	defer func() {
		if r := recover(); r != nil {
			resp = ipc.Errorf("%s", errText(r))
		}
	}()
	h, ok := ops[req.Op]
	if !ok {
		return ipc.Errorf("unknown op: %s", req.Op)
	}
	return h(ctx, req)
}

// request sends an LSP request and panics with an opError on failure.
func (c *Context) request(method string, params any) json.RawMessage {
	res, err := c.Lsp.Request(method, params)
	if err != nil {
		panic(opError{err})
	}
	return res
}

// --- simple ops ---

func opPing(ctx *Context, req ipc.Request) ipc.Response {
	return ipc.Response{OK: true, Pong: true}
}

func opShutdown(ctx *Context, req ipc.Request) ipc.Response {
	// Reply first, then tear down a tick later so the response flushes.
	go func() {
		time.Sleep(50 * time.Millisecond)
		ctx.Shutdown(0)
	}()
	return ipc.Response{OK: true, Message: "shutting down"}
}

func opStatus(ctx *Context, req ipc.Request) ipc.Response {
	return ipc.Response{
		OK:           true,
		Server:       ctx.Server.Name,
		Root:         ctx.Root,
		Pid:          os.Getpid(),
		Socket:       ctx.SocketPath,
		Capabilities: summarizeCapabilities(ctx.Capabilities),
	}
}

// --- positional query ops ---

func opDef(ctx *Context, req ipc.Request) ipc.Response {
	at, errResp := locate(ctx, req)
	if errResp != nil {
		return *errResp
	}
	result := ctx.request("textDocument/definition", positionParams(at))
	return ipc.Response{OK: true, Locations: lsputil.ToSites(result)}
}

func opRefs(ctx *Context, req ipc.Request) ipc.Response {
	at, errResp := locate(ctx, req)
	if errResp != nil {
		return *errResp
	}
	result := ctx.request("textDocument/references", map[string]any{
		"textDocument": map[string]any{"uri": at.uri},
		"position":     at.position,
		"context":      map[string]any{"includeDeclaration": true},
	})
	return ipc.Response{OK: true, Locations: lsputil.ToSites(result)}
}

func opType(ctx *Context, req ipc.Request) ipc.Response {
	at, errResp := locate(ctx, req)
	if errResp != nil {
		return *errResp
	}
	result := ctx.request("textDocument/hover", positionParams(at))
	hover := lsputil.HoverToText(result)
	var hp *string
	if hover != "" {
		hp = &hover
	}
	return ipc.Response{OK: true, Hover: hp}
}

func opMembers(ctx *Context, req ipc.Request) ipc.Response {
	at, errResp := locate(ctx, req)
	if errResp != nil {
		return *errResp
	}
	// The position should sit right after a `.`; ask the server what completes
	// there — i.e. the members of the expression before the dot.
	result := ctx.request("textDocument/completion", map[string]any{
		"textDocument": map[string]any{"uri": at.uri},
		"position":     at.position,
		"context":      map[string]any{"triggerKind": 2, "triggerCharacter": "."},
	})
	items := lsputil.CompletionItems(result)
	if req.Detail {
		items = resolveItems(ctx, items)
	}
	return ipc.Response{
		OK:         true,
		Members:    lsputil.ToMembers(items),
		Incomplete: lsputil.IsIncomplete(result),
	}
}

// resolveItems fills in signatures/docs by resolving each item (one round-trip
// each, bounded), preserving order. Items beyond resolveLimit are dropped, as in
// the original.
func resolveItems(ctx *Context, items []lsputil.CompletionItem) []lsputil.CompletionItem {
	n := len(items)
	if n > resolveLimit {
		n = resolveLimit
	}
	resolved := make([]lsputil.CompletionItem, n)
	sem := make(chan struct{}, 16)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			raw, err := ctx.Lsp.Request("completionItem/resolve", items[i])
			if err != nil {
				resolved[i] = items[i]
				return
			}
			var r lsputil.CompletionItem
			trimmed := strings.TrimSpace(string(raw))
			if trimmed == "" || trimmed == "null" || json.Unmarshal(raw, &r) != nil {
				resolved[i] = items[i]
			} else {
				resolved[i] = r
			}
		}(i)
	}
	wg.Wait()
	return resolved
}

func opOutline(ctx *Context, req ipc.Request) ipc.Response {
	if req.File == "" {
		return ipc.Errorf("missing file")
	}
	doc := syncDoc(ctx, req.File)
	if doc == nil {
		return ipc.Errorf("cannot read file: %s", req.File)
	}
	if doc.fresh {
		ctx.Progress.Settle(progressMax)
	}
	result := ctx.request("textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]any{"uri": doc.uri},
	})
	return ipc.Response{OK: true, Outline: lsputil.ToOutline(result)}
}

func opSymbol(ctx *Context, req ipc.Request) ipc.Response {
	if req.Query == "" {
		return ipc.Errorf("missing query")
	}
	// navto only searches projects that are already loaded, and projects load
	// lazily when a file is opened. So grep the workspace for the query, open the
	// files that mention it — that loads exactly the project(s) the symbol lives
	// in — then run the search.
	loadProjectsMentioning(ctx, req.Query)
	result := ctx.request("workspace/symbol", map[string]any{"query": req.Query})
	return ipc.Response{OK: true, Symbols: lsputil.ToFoundSymbols(result)}
}

func opCallers(ctx *Context, req ipc.Request) ipc.Response {
	at, errResp := locate(ctx, req)
	if errResp != nil {
		return *errResp
	}
	itemsRaw := ctx.request("textDocument/prepareCallHierarchy", positionParams(at))
	var items []json.RawMessage
	json.Unmarshal(itemsRaw, &items)
	if len(items) == 0 {
		return ipc.Errorf("no call hierarchy at this position")
	}
	calls := ctx.request("callHierarchy/incomingCalls", map[string]any{"item": items[0]})
	return ipc.Response{OK: true, Callers: lsputil.ToCallers(calls)}
}

func opCheck(ctx *Context, req ipc.Request) ipc.Response {
	if req.File == "" {
		return ipc.Errorf("missing file")
	}
	doc := syncDoc(ctx, req.File)
	if doc == nil {
		return ipc.Errorf("cannot read file: %s", req.File)
	}
	if doc.fresh {
		ctx.Progress.Settle(progressMax)
	}
	// If we just opened or edited the file, the cached diagnostics are stale —
	// wait for the fresh wave the server is about to push.
	diags := ctx.Diagnostics.Collect(doc.uri, doc.changed, diagDebounce, diagMax)
	return ipc.Response{OK: true, Diagnostics: lsputil.NormalizeDiagnostics(diags)}
}

// --- mutation ops ---

func opRename(ctx *Context, req ipc.Request) ipc.Response {
	at, errResp := locate(ctx, req)
	if errResp != nil {
		return *errResp
	}
	newName := strings.TrimSpace(req.NewName)
	if newName == "" {
		return ipc.Errorf("missing newName")
	}

	// Fail fast if the position can't be renamed. Some servers reject a
	// non-identifier position by erroring rather than returning null; treat
	// either as "not here" but don't let an unsupported prepare block us.
	if prep, err := ctx.Lsp.Request("textDocument/prepareRename", positionParams(at)); err == nil {
		if strings.TrimSpace(string(prep)) == "null" {
			return ipc.Errorf("cannot rename at this position")
		}
	}

	editRaw := ctx.request("textDocument/rename", map[string]any{
		"textDocument": map[string]any{"uri": at.uri},
		"position":     at.position,
		"newName":      newName,
	})
	plan := planFromResult(editRaw)
	if len(plan) == 0 {
		return ipc.Errorf("no rename edits produced")
	}

	sites := planSites(plan)
	resourceOps := resourceOpsOf(plan)
	fileCount := countFiles(plan)

	if req.DryRun {
		return ipc.Response{OK: true, Applied: false, NewName: newName, FileCount: fileCount, Sites: sites, ResourceOps: resourceOps}
	}
	applyPlan(ctx, plan)
	return ipc.Response{OK: true, Applied: true, NewName: newName, FileCount: fileCount, Sites: sites, ResourceOps: resourceOps}
}

func opMv(ctx *Context, req ipc.Request) ipc.Response {
	from, to := req.From, req.To
	if from == "" || to == "" {
		return ipc.Errorf("mv requires from and to")
	}
	if !fileExists(from) {
		return ipc.Errorf("no such file: %s", from)
	}
	if pathExists(to) {
		return ipc.Errorf("target already exists: %s", to)
	}

	// Open the file so the project is loaded; the server needs the import graph
	// to know which files reference the one we're moving.
	doc := syncDoc(ctx, from)
	if doc == nil {
		return ipc.Errorf("cannot read file: %s", from)
	}
	if doc.fresh {
		ctx.Progress.Settle(progressMax)
	}

	oldURI := lsputil.FileToURI(from)
	newURI := lsputil.FileToURI(to)

	// Ask what import edits the move requires (computed for the pre-move state).
	editRaw := ctx.request("workspace/willRenameFiles", map[string]any{
		"files": []map[string]any{{"oldUri": oldURI, "newUri": newURI}},
	})
	plan := planFromResult(editRaw)
	sites := planSites(plan)
	fileCount := countFiles(plan)

	if req.DryRun {
		return ipc.Response{OK: true, Applied: false, From: from, To: to, FileCount: fileCount, Sites: sites}
	}

	// Rewrite imports on the current paths, then move the file on disk.
	applyPlan(ctx, plan)
	os.MkdirAll(filepath.Dir(to), 0o755)
	os.Rename(from, to)

	// Stop tracking the old path and tell the server the file moved.
	if ctx.hasOpen(oldURI) {
		ctx.Lsp.Notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": oldURI}})
		ctx.deleteOpen(oldURI)
	}
	ctx.Lsp.Notify("workspace/didRenameFiles", map[string]any{
		"files": []map[string]any{{"oldUri": oldURI, "newUri": newURI}},
	})

	return ipc.Response{OK: true, Applied: true, From: from, To: to, FileCount: fileCount, Sites: sites}
}

type codeAction struct {
	Title   string                 `json:"title"`
	Kind    string                 `json:"kind"`
	Edit    *lsputil.WorkspaceEdit `json:"edit"`
	Data    json.RawMessage        `json:"data"`
	Command *struct {
		Command   string            `json:"command"`
		Arguments []json.RawMessage `json:"arguments"`
	} `json:"command"`
	raw json.RawMessage
}

func opExtract(ctx *Context, req ipc.Request) ipc.Response {
	if req.File == "" {
		return ipc.Errorf("missing file")
	}
	if !(req.StartLine >= 1 && req.StartCol >= 1 && req.EndLine >= 1 && req.EndCol >= 1) {
		return ipc.Errorf("invalid range")
	}

	doc := syncDoc(ctx, req.File)
	if doc == nil {
		return ipc.Errorf("cannot read file: %s", req.File)
	}
	if doc.fresh {
		ctx.Progress.Settle(progressMax)
	}

	result := ctx.request("textDocument/codeAction", map[string]any{
		"textDocument": map[string]any{"uri": doc.uri},
		"range": map[string]any{
			"start": lsputil.ToLSPPosition(req.StartLine, req.StartCol),
			"end":   lsputil.ToLSPPosition(req.EndLine, req.EndCol),
		},
		"context": map[string]any{"diagnostics": []any{}, "only": []string{"refactor.extract"}},
	})

	candidates := parseCodeActions(result)
	if len(candidates) == 0 {
		return ipc.Errorf("no extract refactoring available for this selection")
	}

	// Choose a candidate. Without --scope, apply only if unambiguous; otherwise
	// list the options so the caller can pick.
	var chosen *codeAction
	if req.Scope != "" {
		chosen = chooseCandidate(candidates, req.Scope)
		if chosen == nil {
			return ipc.Errorf("--scope must be a number from the list (got %q); run without --scope to list", req.Scope)
		}
	} else if len(candidates) == 1 {
		chosen = &candidates[0]
	}
	if chosen == nil {
		cands := make([]ipc.Candidate, len(candidates))
		for i, c := range candidates {
			cands[i] = ipc.Candidate{Index: i + 1, Title: c.Title, Kind: c.Kind}
		}
		return ipc.Response{OK: true, Mode: "list", Candidates: cands}
	}

	edit := resolveEdit(ctx, *chosen)
	if edit == nil {
		return ipc.Errorf("could not obtain the refactoring edit")
	}
	plan := lsputil.PlanWorkspaceEdit(*edit)
	if len(plan) == 0 {
		return ipc.Errorf("refactoring produced no edits")
	}
	sites := planSites(plan)

	if req.DryRun {
		return ipc.Response{OK: true, Mode: "preview", Chosen: chosen.Title, Sites: sites}
	}
	applyPlan(ctx, plan)
	return ipc.Response{OK: true, Mode: "apply", Chosen: chosen.Title, Sites: sites, Placeholder: findPlaceholder(plan)}
}

func parseCodeActions(result json.RawMessage) []codeAction {
	var raw []json.RawMessage
	if json.Unmarshal(result, &raw) != nil {
		return nil
	}
	var out []codeAction
	for _, item := range raw {
		var a codeAction
		if json.Unmarshal(item, &a) != nil || a.Title == "" {
			continue
		}
		a.raw = item
		out = append(out, a)
	}
	return out
}

// chooseCandidate picks a candidate by 1-based index.
func chooseCandidate(candidates []codeAction, scope string) *codeAction {
	n, err := strconv.Atoi(scope)
	if err != nil || n < 1 || n > len(candidates) {
		return nil
	}
	return &candidates[n-1]
}

// resolveEdit gets the WorkspaceEdit for a code action, however the server
// delivers it: inline, via codeAction/resolve, or via a command that triggers a
// server→client applyEdit (typescript-language-server's refactor path).
func resolveEdit(ctx *Context, action codeAction) *lsputil.WorkspaceEdit {
	if action.Edit != nil {
		return action.Edit
	}
	if len(action.Data) > 0 {
		resolvedRaw := ctx.request("codeAction/resolve", action.raw)
		var resolved codeAction
		if json.Unmarshal(resolvedRaw, &resolved) == nil && resolved.Edit != nil {
			return resolved.Edit
		}
	}
	if action.Command != nil {
		editCh := ctx.Lsp.NextApplyEdit()
		args := action.Command.Arguments
		if args == nil {
			args = []json.RawMessage{}
		}
		ctx.request("workspace/executeCommand", map[string]any{
			"command":   action.Command.Command,
			"arguments": args,
		})
		// The applyEdit normally arrives during executeCommand; guard with a timeout.
		select {
		case e := <-editCh:
			if len(e) == 0 || string(e) == "null" {
				return nil
			}
			var we lsputil.WorkspaceEdit
			if json.Unmarshal(e, &we) != nil {
				return nil
			}
			return &we
		case <-time.After(2 * time.Second):
			return nil
		}
	}
	return nil
}

// tsserver names extracted symbols with a `newX` placeholder (newFunction,
// newLocal, newProperty, …). We surface its location so it can be renamed.
var placeholderRe = regexp.MustCompile(`\bnew[A-Z][A-Za-z0-9_]*`)

func findPlaceholder(plan []lsputil.PlanOp) *ipc.Placeholder {
	for _, op := range plan {
		if op.Kind != lsputil.OpEdit {
			continue
		}
		for _, edit := range op.Edits {
			match := placeholderRe.FindString(edit.NewText)
			if match == "" {
				continue
			}
			data, err := os.ReadFile(op.File)
			if err != nil {
				continue
			}
			content := string(data)
			idx := strings.Index(content, match)
			if idx == -1 {
				continue
			}
			line, col := offsetToLineCol(content, idx)
			return &ipc.Placeholder{File: op.File, Name: match, Line: line, Col: col}
		}
	}
	return nil
}

func offsetToLineCol(text string, offset int) (line, col int) {
	line, col = 1, 1
	for i := 0; i < offset && i < len(text); i++ {
		if text[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

// --- plan helpers ---

func planFromResult(raw json.RawMessage) []lsputil.PlanOp {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	var edit lsputil.WorkspaceEdit
	if json.Unmarshal(raw, &edit) != nil {
		return nil
	}
	return lsputil.PlanWorkspaceEdit(edit)
}

// planSites lists edit sites in 1-based form, for reporting what a mutation touched.
func planSites(plan []lsputil.PlanOp) []ipc.EditSite {
	var sites []ipc.EditSite
	for _, op := range plan {
		if op.Kind != lsputil.OpEdit {
			continue
		}
		for _, edit := range op.Edits {
			sites = append(sites, ipc.EditSite{
				File: op.File,
				Line: edit.Range.Start.Line + 1,
				Col:  edit.Range.Start.Character + 1,
			})
		}
	}
	return sites
}

func resourceOpsOf(plan []lsputil.PlanOp) []ipc.ResourceOp {
	var out []ipc.ResourceOp
	for _, op := range plan {
		if op.Kind == lsputil.OpEdit {
			continue
		}
		if op.Kind == lsputil.OpRename {
			out = append(out, ipc.ResourceOp{Kind: "rename", From: op.From, To: op.To})
		} else {
			out = append(out, ipc.ResourceOp{Kind: string(op.Kind), File: op.File})
		}
	}
	return out
}

func countFiles(plan []lsputil.PlanOp) int {
	files := map[string]struct{}{}
	for _, op := range plan {
		if op.Kind == lsputil.OpRename {
			files[op.To] = struct{}{}
		} else {
			files[op.File] = struct{}{}
		}
	}
	return len(files)
}

// applyPlan applies a plan to disk, then reconciles the server's view: close
// files we removed/renamed and push didChange for edited files we still hold
// open, so the daemon's model stays in step with the new on-disk content.
func applyPlan(ctx *Context, plan []lsputil.PlanOp) {
	edited := map[string]struct{}{}
	removed := map[string]struct{}{}

	for _, op := range plan {
		switch op.Kind {
		case lsputil.OpEdit:
			data, err := os.ReadFile(op.File)
			if err != nil {
				panic(opError{err})
			}
			os.WriteFile(op.File, []byte(lsputil.ApplyTextEdits(string(data), op.Edits)), 0o644)
			edited[lsputil.FileToURI(op.File)] = struct{}{}
		case lsputil.OpCreate:
			os.WriteFile(op.File, []byte(""), 0o644)
		case lsputil.OpRename:
			os.Rename(op.From, op.To)
			removed[lsputil.FileToURI(op.From)] = struct{}{}
		case lsputil.OpDelete:
			os.Remove(op.File)
			removed[lsputil.FileToURI(op.File)] = struct{}{}
		}
	}

	for uri := range removed {
		if !ctx.hasOpen(uri) {
			continue
		}
		ctx.Lsp.Notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": uri}})
		ctx.deleteOpen(uri)
	}
	for uri := range edited {
		prev, ok := ctx.getOpen(uri)
		if !ok {
			continue // server isn't tracking it; nothing to sync
		}
		data, err := os.ReadFile(lsputil.URIToFile(uri))
		if err != nil {
			continue
		}
		text := string(data)
		version := prev.version + 1
		ctx.Lsp.Notify("textDocument/didChange", map[string]any{
			"textDocument":   map[string]any{"uri": uri, "version": version},
			"contentChanges": []map[string]any{{"text": text}},
		})
		ctx.setOpen(uri, openDoc{version: version, text: text})
	}
}

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
	exts := map[string]struct{}{}
	for _, e := range ctx.Server.Extensions {
		exts[strings.ToLower(e)] = struct{}{}
	}
	hasExt := func(path string) bool {
		dot := strings.LastIndexByte(path, '.')
		if dot == -1 {
			return false
		}
		_, ok := exts[strings.ToLower(path[dot:])]
		return ok
	}

	files := ripgrepFiles(ctx.Root, query)
	if files == nil {
		files = scanFilesContaining(ctx.Root, query, hasExt)
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

// ripgrepFiles returns files containing query via ripgrep, or nil if ripgrep
// isn't available or errored.
func ripgrepFiles(root, query string) []string {
	cmd := exec.Command("rg", "--files-with-matches", "--fixed-strings", "--", query, root)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return []string{} // 1 = no matches (fine)
		}
		return nil // ripgrep not installed or real error
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

// scanFilesContaining is the fallback: a bounded scan reading files for a
// substring match.
func scanFilesContaining(root, query string, hasExt func(string) bool) []string {
	var matches []string
	stack := []string{root}
	budget := 4000
	for len(stack) > 0 && budget > 0 && len(matches) < openLimit {
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
				if data, err := os.ReadFile(full); err == nil && strings.Contains(string(data), query) {
					matches = append(matches, full)
				}
				if len(matches) >= openLimit {
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
