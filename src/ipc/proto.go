// Package ipc carries the CLI ↔ daemon link: newline-delimited JSON over a Unix
// socket, plus the shared typed request/response. Because the CLI and daemon are
// the same binary, both ends share these structs rather than a dynamic map.
//
//	Request:  { op: string, ... }\n
//	Response: { ok: true, ... } | { ok: false, error: string }\n
package ipc

import (
	"fmt"

	"github.com/srossross/clidit/src/lsputil"
)

// Request is the typed superset of fields any op needs. Unused fields are
// omitted on the wire.
type Request struct {
	Op        string `json:"op"`
	File      string `json:"file,omitempty"`
	Line      int    `json:"line,omitempty"`
	Col       int    `json:"col,omitempty"`
	NewName   string `json:"newName,omitempty"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Query     string `json:"query,omitempty"`
	Scope     string `json:"scope,omitempty"`
	StartLine int    `json:"startLine,omitempty"`
	StartCol  int    `json:"startCol,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
	EndCol    int    `json:"endCol,omitempty"`
	Detail    bool   `json:"detail,omitempty"`
	DryRun    bool   `json:"dryRun,omitempty"`
	// Name filters for list ops (members): case-sensitive prefix + RE2 grep.
	Prefix     string `json:"prefix,omitempty"`
	Grep       string `json:"grep,omitempty"`
	IgnoreCase bool   `json:"ignoreCase,omitempty"`
}

// EditSite is a 1-based edit location, for reporting what a mutation touched.
type EditSite struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
}

// ResourceOp describes a create/rename/delete a mutation entails.
type ResourceOp struct {
	Kind string `json:"kind"`
	File string `json:"file,omitempty"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// Candidate is one extract-refactoring option offered when several apply.
type Candidate struct {
	Index int    `json:"index"`
	Title string `json:"title"`
	Kind  string `json:"kind,omitempty"`
}

// Placeholder is the location of an extracted symbol's generated name.
type Placeholder struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Name string `json:"name"`
}

// Response is the typed superset of every op's reply. `ok` is always present;
// the rest are op-specific and omitted when unset. For extract/rename/mv the
// whole struct is what `--json` prints, so this is the user-visible JSON shape.
type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`

	// ping / status
	Pong         bool            `json:"pong,omitempty"`
	Server       string          `json:"server,omitempty"`
	Root         string          `json:"root,omitempty"`
	Pid          int             `json:"pid,omitempty"`
	Socket       string          `json:"socket,omitempty"`
	Capabilities map[string]bool `json:"capabilities,omitempty"`
	Message      string          `json:"message,omitempty"`

	// positional query results
	Locations   []lsputil.Site            `json:"locations,omitempty"`
	Hover       *string                   `json:"hover,omitempty"` // pointer so cmdType --json can emit null
	Members     []lsputil.Member          `json:"members,omitempty"`
	Incomplete  bool                      `json:"incomplete,omitempty"`
	Outline     []lsputil.OutlineNode     `json:"outline,omitempty"`
	Symbols     []lsputil.FoundSymbol     `json:"symbols,omitempty"`
	Callers     []lsputil.CallerSite      `json:"callers,omitempty"`
	Diagnostics []lsputil.CheckDiagnostic `json:"diagnostics,omitempty"`

	// mutations (rename / mv / extract)
	Applied     bool         `json:"applied,omitempty"`
	NewName     string       `json:"newName,omitempty"`
	FileCount   int          `json:"fileCount,omitempty"`
	Sites       []EditSite   `json:"sites,omitempty"`
	ResourceOps []ResourceOp `json:"resourceOps,omitempty"`
	From        string       `json:"from,omitempty"`
	To          string       `json:"to,omitempty"`
	Mode        string       `json:"mode,omitempty"` // list | preview | apply
	Chosen      string       `json:"chosen,omitempty"`
	Candidates  []Candidate  `json:"candidates,omitempty"`
	Placeholder *Placeholder `json:"placeholder,omitempty"`
}

// Errorf builds an error response.
func Errorf(format string, args ...any) Response {
	return Response{OK: false, Error: fmt.Sprintf(format, args...)}
}
