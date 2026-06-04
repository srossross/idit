package daemon

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/srossross/clidit/src/ipc"
	"github.com/srossross/clidit/src/lsputil"
)

// Cap on how many completion items we resolve for `--detail` (one round-trip each).
const resolveLimit = 200

const (
	progressMax  = 30 * time.Second
	diagDebounce = 350 * time.Millisecond
	diagMax      = 5 * time.Second
)

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
		"string":   opTokenSearch,
		"comment":  opTokenSearch,
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
	// Filter by name before resolving details: resolveItems is bounded and drops
	// items past its cap, so a match must survive filtering first.
	matcher, err := lsputil.NewNameMatcher(req.Prefix, req.Grep, req.IgnoreCase)
	if err != nil {
		return ipc.Errorf("invalid --grep regex: %v", err)
	}
	items = matcher.FilterCompletion(items)
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
	n := min(len(items), resolveLimit)
	resolved := make([]lsputil.CompletionItem, n)
	sem := make(chan struct{}, 16)
	var wg sync.WaitGroup
	for i := range n {
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

// Cap on how many files we request semantic tokens for (one round-trip each).
const tokenSearchFileLimit = 100

// opTokenSearch backs `idit string` and `idit comment`: it finds the regex query
// inside spans the server classifies as the token type named by req.Op ("string"
// or "comment"). It greps the workspace for candidate files, then pulls semantic
// tokens for each and matches the query within the wanted spans.
func opTokenSearch(ctx *Context, req ipc.Request) ipc.Response {
	if req.Query == "" {
		return ipc.Errorf("missing query")
	}
	// Compile once, sharing the pattern (including any (?i) for -i) with the
	// ripgrep prefilter so it never excludes a file the regex would match.
	pattern := req.Query
	if req.IgnoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return ipc.Errorf("invalid regex: %v", err)
	}

	legend, err := lsputil.SemanticLegendFrom(ctx.Capabilities)
	if err != nil {
		// Soft-fail: a server without semantic tokens just contributes no hits,
		// so a mixed-language workspace still returns the other servers' results.
		return ipc.Response{OK: true, Message: ctx.Server.Name + ": semantic tokens unavailable"}
	}

	files := candidateFilesRegex(ctx, pattern, re, tokenSearchFileLimit)
	sites := tokenSearchFiles(ctx, files, legend, req.Op, re)
	return ipc.Response{OK: true, Locations: sites}
}

// tokenSearchFiles syncs every candidate file (settling project load once), then
// fans out the per-file semantic-token requests with bounded concurrency.
func tokenSearchFiles(ctx *Context, files []string, legend lsputil.SemanticLegend, wantType string, re *regexp.Regexp) []lsputil.Site {
	type synced struct{ path, uri string }
	var docs []synced
	fresh := false
	for _, f := range files {
		doc := syncDoc(ctx, f)
		if doc == nil {
			continue
		}
		if doc.fresh {
			fresh = true
		}
		docs = append(docs, synced{path: f, uri: doc.uri})
	}
	// A cold open may kick off project loading; settle once before querying so
	// tokens reflect a fully-loaded project.
	if fresh {
		ctx.Progress.Settle(progressMax)
	}

	results := make([][]lsputil.Site, len(docs))
	sem := make(chan struct{}, 16)
	var wg sync.WaitGroup
	for i, d := range docs {
		wg.Add(1)
		go func(i int, d synced) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = scanFileTokens(ctx, d.path, d.uri, legend, wantType, re)
		}(i, d)
	}
	wg.Wait()

	var out []lsputil.Site
	for _, r := range results {
		out = append(out, r...)
	}
	return out
}

// scanFileTokens pulls one file's semantic tokens and returns a Site for every
// regex match inside a span of the wanted type. A per-file error is swallowed so
// one untokenizable file doesn't abort the whole search.
func scanFileTokens(ctx *Context, absPath, uri string, legend lsputil.SemanticLegend, wantType string, re *regexp.Regexp) []lsputil.Site {
	raw, err := ctx.Lsp.Request("textDocument/semanticTokens/full", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
	if err != nil {
		return nil
	}
	open, ok := ctx.getOpen(uri)
	if !ok {
		return nil
	}
	lines := strings.Split(open.text, "\n")
	var sites []lsputil.Site
	for _, tok := range lsputil.DecodeSemanticTokens(raw, legend) {
		if tok.TypeName != wantType {
			continue
		}
		sites = append(sites, lsputil.SitesForRegexInToken(absPath, tok, lines, re)...)
	}
	return sites
}

func opCallers(ctx *Context, req ipc.Request) ipc.Response {
	at, errResp := locate(ctx, req)
	if errResp != nil {
		return *errResp
	}
	itemsRaw := ctx.request("textDocument/prepareCallHierarchy", positionParams(at))
	var items []json.RawMessage
	_ = json.Unmarshal(itemsRaw, &items) // malformed → empty → handled below
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
