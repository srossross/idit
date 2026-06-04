package lsputil

import (
	"encoding/json"
	"sort"
)

// A WorkspaceEdit is how the server describes a multi-file mutation (rename,
// organize-imports, extract, …). This module turns one into a flat, ordered plan
// and applies text edits to file content. Applying to disk + keeping the server
// in sync lives in the daemon (it needs the LSP connection); this part is pure.

type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// WorkspaceEdit decodes the two shapes a server may send: a `changes` map keyed
// by URI, or an ordered `documentChanges` list. documentChanges entries are
// either a text-document edit or a resource op (create/rename/delete), kept as
// raw so they can be discriminated by their `kind`.
type WorkspaceEdit struct {
	Changes         map[string][]TextEdit `json:"changes"`
	DocumentChanges []json.RawMessage     `json:"documentChanges"`
}

// PlanOpKind enumerates the filesystem operations a plan may contain.
type PlanOpKind string

const (
	OpEdit   PlanOpKind = "edit"
	OpCreate PlanOpKind = "create"
	OpRename PlanOpKind = "rename"
	OpDelete PlanOpKind = "delete"
)

// PlanOp is one filesystem operation. File is set for edit/create/delete; From
// and To are set for rename; Edits is set for edit.
type PlanOp struct {
	Kind  PlanOpKind
	File  string
	From  string
	To    string
	Edits []TextEdit
}

// PlanWorkspaceEdit flattens a WorkspaceEdit into an ordered list of filesystem
// operations.
func PlanWorkspaceEdit(edit WorkspaceEdit) []PlanOp {
	if edit.DocumentChanges != nil {
		ops := make([]PlanOp, 0, len(edit.DocumentChanges))
		for _, raw := range edit.DocumentChanges {
			ops = append(ops, planDocumentChange(raw))
		}
		return ops
	}
	if edit.Changes != nil {
		// Object key order is non-deterministic in Go; the original iterated in
		// insertion order, but the daemon applies edits per-file independently
		// so order across files does not affect correctness.
		ops := make([]PlanOp, 0, len(edit.Changes))
		for uri, edits := range edit.Changes {
			ops = append(ops, PlanOp{Kind: OpEdit, File: URIToFile(uri), Edits: edits})
		}
		return ops
	}
	return nil
}

func planDocumentChange(raw json.RawMessage) PlanOp {
	var probe struct {
		Kind string `json:"kind"`
	}
	// Each decode below tolerates malformed input: a parse failure leaves the
	// zero value, yielding a PlanOp with empty paths that callers skip.
	_ = json.Unmarshal(raw, &probe)
	switch probe.Kind {
	case "create":
		var c struct {
			URI string `json:"uri"`
		}
		_ = json.Unmarshal(raw, &c)
		return PlanOp{Kind: OpCreate, File: URIToFile(c.URI)}
	case "rename":
		var r struct {
			OldURI string `json:"oldUri"`
			NewURI string `json:"newUri"`
		}
		_ = json.Unmarshal(raw, &r)
		return PlanOp{Kind: OpRename, From: URIToFile(r.OldURI), To: URIToFile(r.NewURI)}
	case "delete":
		var d struct {
			URI string `json:"uri"`
		}
		_ = json.Unmarshal(raw, &d)
		return PlanOp{Kind: OpDelete, File: URIToFile(d.URI)}
	default:
		var e struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Edits []TextEdit `json:"edits"`
		}
		_ = json.Unmarshal(raw, &e)
		return PlanOp{Kind: OpEdit, File: URIToFile(e.TextDocument.URI), Edits: e.Edits}
	}
}

// ApplyTextEdits applies text edits to a string. Edits are applied bottom-to-top
// (highest offset first) so each splice can't invalidate the offsets of edits
// not yet applied. LSP guarantees edits within one document don't overlap.
func ApplyTextEdits(content string, edits []TextEdit) string {
	lineStarts := lineStartOffsets(content)
	offsetOf := func(line, character int) int {
		var base int
		if line < len(lineStarts) {
			base = lineStarts[line]
		} else {
			base = len(content)
		}
		off := base + character
		if off > len(content) {
			return len(content)
		}
		return off
	}

	ordered := make([]TextEdit, len(edits))
	copy(ordered, edits)
	sort.SliceStable(ordered, func(i, j int) bool {
		oi := offsetOf(ordered[i].Range.Start.Line, ordered[i].Range.Start.Character)
		oj := offsetOf(ordered[j].Range.Start.Line, ordered[j].Range.Start.Character)
		return oi > oj // highest offset first
	})

	out := content
	for _, edit := range ordered {
		start := offsetOf(edit.Range.Start.Line, edit.Range.Start.Character)
		end := offsetOf(edit.Range.End.Line, edit.Range.End.Character)
		out = out[:start] + edit.NewText + out[end:]
	}
	return out
}

func lineStartOffsets(text string) []int {
	starts := []int{0}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}
