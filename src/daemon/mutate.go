package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/srossross/clidit/src/ipc"
	"github.com/srossross/clidit/src/lsputil"
)

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
	//nolint:gosec // a dir in the user's project tree — 0755 matches conventional source-tree perms
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		panic(opError{err})
	}
	if err := os.Rename(from, to); err != nil {
		panic(opError{err})
	}

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
	if req.StartLine < 1 || req.StartCol < 1 || req.EndLine < 1 || req.EndCol < 1 {
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
			//nolint:gosec // op.File is a workspace source path from the LSP edit plan
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
