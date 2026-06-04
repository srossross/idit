package lsputil

import (
	"encoding/json"
	"strings"
)

// Shared handling for the symbol-shaped LSP results: documentSymbol (file
// outline), workspace/symbol (project search), and callHierarchy (callers).

// LSP SymbolKind (1-26) → readable names.
var symbolKind = map[int]string{
	1: "file", 2: "module", 3: "namespace", 4: "package", 5: "class",
	6: "method", 7: "property", 8: "field", 9: "constructor", 10: "enum",
	11: "interface", 12: "function", 13: "variable", 14: "constant", 15: "string",
	16: "number", 17: "boolean", 18: "array", 19: "object", 20: "key",
	21: "null", 22: "enum-member", 23: "struct", 24: "event", 25: "operator",
	26: "type-param",
}

func kindName(kind int) string {
	if name, ok := symbolKind[kind]; ok {
		return name
	}
	return "?"
}

// --- documentSymbol (file outline) ---

type documentSymbol struct {
	Name           string           `json:"name"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []documentSymbol `json:"children"`
}

type symbolInformation struct {
	Name          string   `json:"name"`
	Kind          int      `json:"kind"`
	Location      Location `json:"location"`
	ContainerName string   `json:"containerName"`
}

type OutlineNode struct {
	Name     string        `json:"name"`
	Kind     string        `json:"kind"`
	Line     int           `json:"line"`
	Col      int           `json:"col"`
	Children []OutlineNode `json:"children"`
}

// ToOutline normalizes a documentSymbol result (either response shape) into a
// tree. SymbolInformation[] is flat and carries `location`; DocumentSymbol[]
// nests.
func ToOutline(result json.RawMessage) []OutlineNode {
	var raw []json.RawMessage
	if json.Unmarshal(result, &raw) != nil || len(raw) == 0 {
		return []OutlineNode{}
	}

	if isSymbolInformation(raw[0]) {
		out := make([]OutlineNode, 0, len(raw))
		for _, item := range raw {
			var s symbolInformation
			if json.Unmarshal(item, &s) != nil {
				continue
			}
			out = append(out, OutlineNode{
				Name:     s.Name,
				Kind:     kindName(s.Kind),
				Line:     s.Location.Range.Start.Line + 1,
				Col:      s.Location.Range.Start.Character + 1,
				Children: []OutlineNode{},
			})
		}
		return out
	}

	out := make([]OutlineNode, 0, len(raw))
	for _, item := range raw {
		var s documentSymbol
		if json.Unmarshal(item, &s) != nil {
			continue
		}
		out = append(out, convertDocumentSymbol(s))
	}
	return out
}

func isSymbolInformation(raw json.RawMessage) bool {
	var probe struct {
		Location *json.RawMessage `json:"location"`
	}
	return json.Unmarshal(raw, &probe) == nil && probe.Location != nil
}

func convertDocumentSymbol(s documentSymbol) OutlineNode {
	children := make([]OutlineNode, 0, len(s.Children))
	for _, c := range s.Children {
		children = append(children, convertDocumentSymbol(c))
	}
	return OutlineNode{
		Name:     s.Name,
		Kind:     kindName(s.Kind),
		Line:     s.SelectionRange.Start.Line + 1,
		Col:      s.SelectionRange.Start.Character + 1,
		Children: children,
	}
}

// --- workspace/symbol (project search) ---

type FoundSymbol struct {
	File      string `json:"file"`
	Line      int    `json:"line"`
	Col       int    `json:"col"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Container string `json:"container,omitempty"`
}

func ToFoundSymbols(result json.RawMessage) []FoundSymbol {
	var arr []symbolInformation
	trimmed := strings.TrimSpace(string(result))
	if trimmed == "" || trimmed == "null" || json.Unmarshal(result, &arr) != nil {
		return nil
	}
	out := make([]FoundSymbol, 0, len(arr))
	for _, s := range arr {
		r := s.Location.Range
		out = append(out, FoundSymbol{
			File:      URIToFile(s.Location.URI),
			Line:      r.Start.Line + 1,
			Col:       r.Start.Character + 1,
			Kind:      kindName(s.Kind),
			Name:      s.Name,
			Container: s.ContainerName,
		})
	}
	return out
}

// --- callHierarchy (callers) ---

type callHierarchyItem struct {
	Name           string `json:"name"`
	Kind           int    `json:"kind"`
	URI            string `json:"uri"`
	Range          Range  `json:"range"`
	SelectionRange Range  `json:"selectionRange"`
}

type incomingCall struct {
	From       callHierarchyItem `json:"from"`
	FromRanges []Range           `json:"fromRanges"`
}

type CallerSite struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// ToCallers flattens incoming calls into one site per actual call location.
func ToCallers(result json.RawMessage) []CallerSite {
	var calls []incomingCall
	trimmed := strings.TrimSpace(string(result))
	if trimmed == "" || trimmed == "null" || json.Unmarshal(result, &calls) != nil {
		return nil
	}
	var sites []CallerSite
	for _, call := range calls {
		from := call.From
		name := from.Name
		kind := kindName(from.Kind)
		if len(call.FromRanges) == 0 {
			// No precise call sites: fall back to the caller's own location.
			sites = append(sites, CallerSite{
				File: URIToFile(from.URI),
				Line: from.SelectionRange.Start.Line + 1,
				Col:  from.SelectionRange.Start.Character + 1,
				Name: name,
				Kind: kind,
			})
			continue
		}
		for _, r := range call.FromRanges {
			sites = append(sites, CallerSite{
				File: URIToFile(from.URI),
				Line: r.Start.Line + 1,
				Col:  r.Start.Character + 1,
				Name: name,
				Kind: kind,
			})
		}
	}
	return sites
}
