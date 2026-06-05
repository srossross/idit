package daemon

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/srossross/idit/src/ipc"
	"github.com/srossross/idit/src/lsputil"
)

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
			//nolint:gosec // op.File is a workspace source path produced by the LSP edit plan
			data, err := os.ReadFile(op.File)
			if err != nil {
				panic(opError{err})
			}
			updated := lsputil.ApplyTextEdits(string(data), op.Edits)
			//nolint:gosec // rewriting a user source file — 0644 is the conventional source-file mode
			if err := os.WriteFile(op.File, []byte(updated), 0o644); err != nil {
				panic(opError{err})
			}
			edited[lsputil.FileToURI(op.File)] = struct{}{}
		case lsputil.OpCreate:
			//nolint:gosec // creating a user source file — 0644 is the conventional source-file mode
			if err := os.WriteFile(op.File, []byte(""), 0o644); err != nil {
				panic(opError{err})
			}
		case lsputil.OpRename:
			if err := os.Rename(op.From, op.To); err != nil {
				panic(opError{err})
			}
			removed[lsputil.FileToURI(op.From)] = struct{}{}
		case lsputil.OpDelete:
			if err := os.Remove(op.File); err != nil {
				panic(opError{err})
			}
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
		//nolint:gosec // uri refers to a workspace file we just edited
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
