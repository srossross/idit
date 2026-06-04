package lsputil

import (
	"encoding/json"
	"strings"
)

// `textDocument/hover` returns `contents` in one of several shapes:
//   - a plain string (legacy MarkedString)
//   - { language, value } (legacy MarkedString) → a fenced code block
//   - MarkupContent { kind: "markdown" | "plaintext", value }
//   - an array of any of the above
//
// Flatten all of them into one markdown string. HoverToText returns the empty
// string when there is no usable hover text (the caller treats that as null).

func HoverToText(result json.RawMessage) string {
	var hover struct {
		Contents json.RawMessage `json:"contents"`
	}
	if json.Unmarshal(result, &hover) != nil || len(hover.Contents) == 0 {
		return ""
	}
	return strings.TrimSpace(markupToText(hover.Contents))
}

func markupToText(contents json.RawMessage) string {
	trimmed := strings.TrimSpace(string(contents))
	if trimmed == "" || trimmed == "null" {
		return ""
	}

	switch trimmed[0] {
	case '"':
		var s string
		if json.Unmarshal(contents, &s) == nil {
			return s
		}
		return ""
	case '[':
		var arr []json.RawMessage
		if json.Unmarshal(contents, &arr) != nil {
			return ""
		}
		parts := make([]string, len(arr))
		for i, item := range arr {
			parts[i] = markupToText(item)
		}
		return strings.Join(parts, "\n")
	default:
		var obj struct {
			Value    *string `json:"value"`
			Language *string `json:"language"`
		}
		if json.Unmarshal(contents, &obj) != nil || obj.Value == nil {
			return ""
		}
		// MarkedString carries a `language`; render it as a fenced block. Plain
		// MarkupContent already contains its own markdown.
		if obj.Language != nil {
			return "```" + *obj.Language + "\n" + *obj.Value + "\n```"
		}
		return *obj.Value
	}
}
