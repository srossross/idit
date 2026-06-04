package lsputil

import (
	"encoding/json"
	"sort"
	"strings"
)

// `textDocument/completion` returns either a bare CompletionItem[] or a
// CompletionList. For `members`, we request completion right after a `.` and
// present the items — the members accessible on the expression before the dot.

type CompletionItem struct {
	Label    string `json:"label"`
	Kind     int    `json:"kind,omitempty"`
	Detail   string `json:"detail,omitempty"`
	SortText string `json:"sortText,omitempty"`
	// Data carries server-defined fields needed by completionItem/resolve.
	Data json.RawMessage `json:"data,omitempty"`
}

type completionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []CompletionItem `json:"items"`
}

type Member struct {
	Label  string `json:"label"`
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
}

// LSP CompletionItemKind (1-25) → readable names.
var completionKind = map[int]string{
	1: "text", 2: "method", 3: "function", 4: "constructor", 5: "field",
	6: "variable", 7: "class", 8: "interface", 9: "module", 10: "property",
	11: "unit", 12: "value", 13: "enum", 14: "keyword", 15: "snippet",
	16: "color", 17: "file", 18: "reference", 19: "folder", 20: "enum-member",
	21: "constant", 22: "struct", 23: "event", 24: "operator", 25: "type-param",
}

// CompletionItems extracts the items from either response shape.
func CompletionItems(result json.RawMessage) []CompletionItem {
	trimmed := strings.TrimSpace(string(result))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if trimmed[0] == '[' {
		var arr []CompletionItem
		json.Unmarshal(result, &arr)
		return arr
	}
	var list completionList
	json.Unmarshal(result, &list)
	return list.Items
}

// IsIncomplete reports whether a CompletionList marked itself incomplete.
func IsIncomplete(result json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(result))
	if trimmed == "" || trimmed == "null" || trimmed[0] == '[' {
		return false
	}
	var list completionList
	json.Unmarshal(result, &list)
	return list.IsIncomplete
}

// ToMembers normalizes + sorts completion items into members, by the server's
// ranking (sortText, falling back to label).
func ToMembers(items []CompletionItem) []Member {
	sorted := make([]CompletionItem, len(items))
	copy(sorted, items)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sortKey(sorted[i]) < sortKey(sorted[j])
	})
	members := make([]Member, len(sorted))
	for i, item := range sorted {
		kind := completionKind[item.Kind]
		if kind == "" {
			kind = "?"
		}
		members[i] = Member{Label: item.Label, Kind: kind, Detail: item.Detail}
	}
	return members
}

func sortKey(item CompletionItem) string {
	if item.SortText != "" {
		return item.SortText
	}
	return item.Label
}
