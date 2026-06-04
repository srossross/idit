package lsputil

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// LSP semantic tokens are how the server tells us which spans of a file are
// strings, comments, keywords, etc. The server advertises a legend (the ordered
// list of token-type names) in its initialize response, and returns the tokens
// themselves as a flat, delta-encoded []uint: every 5 ints describe one token as
// (deltaLine, deltaStartChar, length, tokenTypeIndex, tokenModifiers), with line
// and start-char accumulated relative to the previous token. Offsets are in
// UTF-16 code units, matching the rest of LSP.

// ErrNoSemanticTokens means the server did not advertise a semanticTokensProvider
// with a usable legend, so string/comment classification is unavailable.
var ErrNoSemanticTokens = errors.New("server does not support semantic tokens")

// SemanticToken is one decoded token in absolute, 0-based coordinates. StartChar
// and Length are UTF-16 code units (LSP's native unit).
type SemanticToken struct {
	Line      int
	StartChar int
	Length    int
	TypeName  string // resolved through the legend; "" if the index is out of range
}

// SemanticLegend is the token-type vocabulary the server returned in its
// initialize response. Index into TokenTypes is what tokens carry.
type SemanticLegend struct {
	TokenTypes []string
}

// SemanticLegendFrom digs the legend out of the raw server initialize
// capabilities (capabilities.semanticTokensProvider.legend.tokenTypes). It
// returns ErrNoSemanticTokens when the provider is absent, disabled, or carries
// no token types.
func SemanticLegendFrom(caps map[string]any) (SemanticLegend, error) {
	provRaw, ok := caps["semanticTokensProvider"]
	if !ok || provRaw == nil {
		return SemanticLegend{}, ErrNoSemanticTokens
	}
	prov, ok := provRaw.(map[string]any)
	if !ok {
		return SemanticLegend{}, ErrNoSemanticTokens
	}
	legend, ok := prov["legend"].(map[string]any)
	if !ok {
		return SemanticLegend{}, ErrNoSemanticTokens
	}
	types := toStringSlice(legend["tokenTypes"])
	if len(types) == 0 {
		return SemanticLegend{}, ErrNoSemanticTokens
	}
	return SemanticLegend{TokenTypes: types}, nil
}

// toStringSlice coerces the []any-of-strings shape that json.Unmarshal into
// map[string]any produces back into a []string, skipping non-strings.
func toStringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// DecodeSemanticTokens turns a textDocument/semanticTokens/full result
// ({"data":[...]}) into absolute tokens, resolving each type index through the
// legend. A malformed or empty payload yields nil; a token whose type index is
// out of the legend's range gets an empty TypeName rather than being dropped.
func DecodeSemanticTokens(result json.RawMessage, legend SemanticLegend) []SemanticToken {
	var payload struct {
		Data []uint32 `json:"data"`
	}
	if json.Unmarshal(result, &payload) != nil {
		return nil
	}
	data := payload.Data
	if len(data) < 5 {
		return nil
	}
	toks := make([]SemanticToken, 0, len(data)/5)
	line, char := 0, 0
	for i := 0; i+4 < len(data); i += 5 {
		dLine := int(data[i])
		dChar := int(data[i+1])
		length := int(data[i+2])
		typeIdx := int(data[i+3])
		// data[i+4] is the modifier bitset, unused for string/comment filtering.
		if dLine == 0 {
			char += dChar // same line: start-char is relative to the previous token
		} else {
			line += dLine // new line: start-char resets to an absolute offset
			char = dChar
		}
		name := ""
		if typeIdx >= 0 && typeIdx < len(legend.TokenTypes) {
			name = legend.TokenTypes[typeIdx]
		}
		toks = append(toks, SemanticToken{Line: line, StartChar: char, Length: length, TypeName: name})
	}
	return toks
}

// tokenSegment is the byte range a token covers on a single physical line.
type tokenSegment struct {
	line      int // 0-based line index
	byteStart int
	byteEnd   int
}

// tokenSegments resolves a token (whose start/length are in UTF-16 units) into
// the concrete byte ranges it spans, one per physical line. Most tokens are
// single-line; a multiline token consumes one unit per line break as it walks
// onto each following line.
func tokenSegments(tok SemanticToken, lines []string) []tokenSegment {
	if tok.Line < 0 || tok.Line >= len(lines) || tok.Length <= 0 {
		return nil
	}
	var segs []tokenSegment
	remaining := tok.Length
	lineIdx := tok.Line
	col := tok.StartChar
	for remaining > 0 && lineIdx < len(lines) {
		line := lines[lineIdx]
		avail := max(byteToUTF16(line, len(line))-col, 0)
		take := min(remaining, avail)
		segs = append(segs, tokenSegment{
			line:      lineIdx,
			byteStart: utf16ToByte(line, col),
			byteEnd:   utf16ToByte(line, col+take),
		})
		remaining -= take
		if remaining <= 0 {
			break
		}
		remaining-- // the newline between this line and the next
		lineIdx++
		col = 0
	}
	return segs
}

// TokenText returns the source text the token covers, joining physical lines with
// "\n" for a multiline token.
func TokenText(tok SemanticToken, lines []string) string {
	segs := tokenSegments(tok, lines)
	if len(segs) == 0 {
		return ""
	}
	parts := make([]string, len(segs))
	for i, s := range segs {
		parts[i] = lines[s.line][s.byteStart:s.byteEnd]
	}
	return strings.Join(parts, "\n")
}

// SitesForRegexInToken returns a Site for every match of re inside the token's
// text. Each Site points at the exact match start (1-based line:col, columns in
// UTF-16 units to match the rest of idit's output), and carries the token's full
// span as Range. Matching runs per physical line so reported positions are always
// real.
func SitesForRegexInToken(file string, tok SemanticToken, lines []string, re *regexp.Regexp) []Site {
	segs := tokenSegments(tok, lines)
	if len(segs) == 0 {
		return nil
	}
	last := segs[len(segs)-1]
	span := Span{
		StartLine: tok.Line + 1,
		StartCol:  tok.StartChar + 1,
		EndLine:   last.line + 1,
		EndCol:    byteToUTF16(lines[last.line], last.byteEnd) + 1,
	}
	var sites []Site
	for _, seg := range segs {
		line := lines[seg.line]
		sub := line[seg.byteStart:seg.byteEnd]
		for _, m := range re.FindAllStringIndex(sub, -1) {
			col := byteToUTF16(line, seg.byteStart+m[0]) + 1
			sites = append(sites, Site{File: file, Line: seg.line + 1, Col: col, Range: span})
		}
	}
	return sites
}

// utf16ToByte maps a UTF-16 code-unit offset into line to a byte offset, clamping
// at the end of the line. Runes above the BMP count as two UTF-16 units.
func utf16ToByte(line string, u16 int) int {
	if u16 <= 0 {
		return 0
	}
	units := 0
	for i, r := range line {
		if units >= u16 {
			return i
		}
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	return len(line)
}

// byteToUTF16 maps a byte offset into line to a UTF-16 code-unit offset.
func byteToUTF16(line string, b int) int {
	units := 0
	for i, r := range line {
		if i >= b {
			break
		}
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	return units
}
