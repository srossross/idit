package lsputil

import (
	"encoding/json"
	"strings"
)

// LSP speaks `file://` URIs and 0-based line/character positions. idit's CLI
// speaks filesystem paths and 1-based line:col (editor convention). These
// helpers translate between the two worlds.

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

type LocationLink struct {
	TargetURI            string `json:"targetUri"`
	TargetRange          Range  `json:"targetRange"`
	TargetSelectionRange *Range `json:"targetSelectionRange"`
}

// Span is the full 1-based span of a declaration.
type Span struct {
	StartLine int `json:"startLine"`
	StartCol  int `json:"startCol"`
	EndLine   int `json:"endLine"`
	EndCol    int `json:"endCol"`
}

// Site is a definition/reference site in idit's 1-based, path-based form.
type Site struct {
	File string `json:"file"`
	// Line/Col are the "jump here" position — the symbol name (1-based).
	Line int `json:"line"`
	Col  int `json:"col"`
	// Range is the full declaration range, for rendering a source preview.
	Range Span `json:"range"`
}

const hexDigits = "0123456789ABCDEF"

// shouldEncode reports whether a byte must be percent-encoded to match Node's
// pathToFileURL on POSIX. That is the WHATWG path percent-encode set (C0
// controls, bytes > 0x7E, and space " # < > ? ` { }) plus `%` and `\`, which
// Node escapes explicitly before assigning the URL pathname.
func shouldEncode(b byte) bool {
	if b <= 0x1F || b >= 0x7F {
		return true
	}
	switch b {
	case ' ', '"', '#', '<', '>', '?', '`', '{', '}', '%', '\\':
		return true
	}
	return false
}

// FileToURI converts an absolute filesystem path to a `file://` URI, matching
// Node's pathToFileURL(path).href byte-for-byte on POSIX. The path must already
// be absolute (callers resolve before calling).
func FileToURI(absPath string) string {
	var b strings.Builder
	b.WriteString("file://")
	for i := 0; i < len(absPath); i++ {
		c := absPath[i]
		if shouldEncode(c) {
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0x0F])
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// URIToFile converts a `file://` URI back to a filesystem path, percent-decoding
// permissively (mirrors Node's fileURLToPath for empty-host file URIs).
func URIToFile(uri string) string {
	rest := strings.TrimPrefix(uri, "file://")
	// A non-empty host would precede the path; on POSIX the path is everything
	// from the first slash. file:///path leaves rest == "/path" (empty host).
	if !strings.HasPrefix(rest, "/") {
		if slash := strings.IndexByte(rest, '/'); slash != -1 {
			rest = rest[slash:]
		}
	}
	return percentDecode(rest)
}

func percentDecode(s string) string {
	if !strings.ContainsRune(s, '%') {
		return s
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			hi, ok1 := fromHex(s[i+1])
			lo, ok2 := fromHex(s[i+2])
			if ok1 && ok2 {
				out = append(out, hi<<4|lo)
				i += 2
				continue
			}
		}
		out = append(out, s[i])
	}
	return string(out)
}

func fromHex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// ToLSPPosition converts 1-based line:col to a 0-based LSP position.
func ToLSPPosition(line, col int) Position {
	return Position{Line: line - 1, Character: col - 1}
}

// defItem is the union of Location and LocationLink fields, so one struct can
// decode whichever shape the server returns.
type defItem struct {
	URI                  string `json:"uri"`
	Range                Range  `json:"range"`
	TargetURI            string `json:"targetUri"`
	TargetRange          Range  `json:"targetRange"`
	TargetSelectionRange *Range `json:"targetSelectionRange"`
}

// ToSites normalizes a textDocument/definition (or similar) result — which may
// be null, a single Location, a Location array, or a LocationLink array — into
// Sites.
func ToSites(result json.RawMessage) []Site {
	items := decodeArrayOrSingle[defItem](result)
	sites := make([]Site, 0, len(items))
	for _, item := range items {
		if item.TargetURI != "" {
			// LocationLink: targetRange is the whole declaration,
			// targetSelectionRange is just the name. Jump to the name; preview
			// the whole thing.
			full := item.TargetRange
			selection := full
			if item.TargetSelectionRange != nil {
				selection = *item.TargetSelectionRange
			}
			sites = append(sites, siteFrom(item.TargetURI, selection, full))
			continue
		}
		sites = append(sites, siteFrom(item.URI, item.Range, item.Range))
	}
	return sites
}

// decodeArrayOrSingle unmarshals a JSON value that may be null, a single object,
// or an array of objects into a []T. Mirrors the TS `Array.isArray(x) ? x : [x]`
// normalization used across the LSP result handlers.
func decodeArrayOrSingle[T any](result json.RawMessage) []T {
	trimmed := strings.TrimSpace(string(result))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if trimmed[0] == '[' {
		var arr []T
		if json.Unmarshal(result, &arr) != nil {
			return nil
		}
		return arr
	}
	var single T
	if json.Unmarshal(result, &single) != nil {
		return nil
	}
	return []T{single}
}

func siteFrom(uri string, selection, full Range) Site {
	return Site{
		File: URIToFile(uri),
		Line: selection.Start.Line + 1,
		Col:  selection.Start.Character + 1,
		Range: Span{
			StartLine: full.Start.Line + 1,
			StartCol:  full.Start.Character + 1,
			EndLine:   full.End.Line + 1,
			EndCol:    full.End.Character + 1,
		},
	}
}
