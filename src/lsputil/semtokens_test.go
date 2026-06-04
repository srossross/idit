package lsputil

import (
	"encoding/json"
	"reflect"
	"regexp"
	"testing"
)

func TestDecodeSemanticTokens(t *testing.T) {
	// The canonical example from the LSP spec: three tokens, the second on the
	// same line as the first (deltaLine 0, start-char relative), the third three
	// lines down (start-char absolute again).
	legend := SemanticLegend{TokenTypes: []string{"property", "type", "class"}}
	data := []uint32{
		2, 5, 3, 0, 3,
		0, 5, 4, 1, 0,
		3, 2, 7, 2, 0,
	}
	raw, _ := json.Marshal(map[string]any{"data": data})
	got := DecodeSemanticTokens(raw, legend)
	want := []SemanticToken{
		{Line: 2, StartChar: 5, Length: 3, TypeName: "property"},
		{Line: 2, StartChar: 10, Length: 4, TypeName: "type"},
		{Line: 5, StartChar: 2, Length: 7, TypeName: "class"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DecodeSemanticTokens = %+v, want %+v", got, want)
	}
}

func TestDecodeSemanticTokensOutOfRangeType(t *testing.T) {
	legend := SemanticLegend{TokenTypes: []string{"string"}}
	raw, _ := json.Marshal(map[string]any{"data": []uint32{0, 0, 2, 9, 0}})
	got := DecodeSemanticTokens(raw, legend)
	if len(got) != 1 || got[0].TypeName != "" {
		t.Fatalf("out-of-range type index should yield empty TypeName, got %+v", got)
	}
}

func TestDecodeSemanticTokensMalformed(t *testing.T) {
	legend := SemanticLegend{TokenTypes: []string{"string"}}
	for _, in := range []string{`null`, `{}`, `{"data":[]}`, `{"data":[1,2,3]}`, `not json`} {
		if got := DecodeSemanticTokens(json.RawMessage(in), legend); got != nil {
			t.Errorf("DecodeSemanticTokens(%s) = %+v, want nil", in, got)
		}
	}
}

func TestSemanticLegendFrom(t *testing.T) {
	cases := []struct {
		name    string
		caps    map[string]any
		wantErr bool
		want    []string
	}{
		{
			name: "object with legend",
			caps: map[string]any{"semanticTokensProvider": map[string]any{
				"legend": map[string]any{"tokenTypes": []any{"comment", "string"}},
			}},
			want: []string{"comment", "string"},
		},
		{name: "provider false", caps: map[string]any{"semanticTokensProvider": false}, wantErr: true},
		{name: "provider true", caps: map[string]any{"semanticTokensProvider": true}, wantErr: true},
		{name: "missing provider", caps: map[string]any{}, wantErr: true},
		{name: "missing legend", caps: map[string]any{"semanticTokensProvider": map[string]any{}}, wantErr: true},
		{
			name: "empty token types",
			caps: map[string]any{"semanticTokensProvider": map[string]any{
				"legend": map[string]any{"tokenTypes": []any{}},
			}},
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := SemanticLegendFrom(c.caps)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got legend %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got.TokenTypes, c.want) {
				t.Fatalf("TokenTypes = %v, want %v", got.TokenTypes, c.want)
			}
		})
	}
}

func TestSitesForRegexInToken(t *testing.T) {
	// `x := "hello TODO world"` — a string token covering the quoted literal.
	lines := []string{`x := "hello TODO world"`}
	tok := SemanticToken{Line: 0, StartChar: 5, Length: 18, TypeName: "string"}

	sites := SitesForRegexInToken("/f.go", tok, lines, regexp.MustCompile(`TODO`))
	if len(sites) != 1 || sites[0].Line != 1 || sites[0].Col != 13 {
		t.Fatalf("single match: got %+v, want one site at 1:13", sites)
	}
	if sites[0].Range != (Span{StartLine: 1, StartCol: 6, EndLine: 1, EndCol: 24}) {
		t.Fatalf("range = %+v", sites[0].Range)
	}

	// Case-insensitive via (?i).
	ci := SitesForRegexInToken("/f.go", tok, lines, regexp.MustCompile(`(?i)todo`))
	if len(ci) != 1 || ci[0].Col != 13 {
		t.Fatalf("case-insensitive match: got %+v", ci)
	}

	// Multiple matches within one token.
	multi := SitesForRegexInToken("/f.go",
		SemanticToken{Line: 0, StartChar: 0, Length: 7, TypeName: "string"},
		[]string{`a x a x`}, regexp.MustCompile(`a`))
	if len(multi) != 2 || multi[0].Col != 1 || multi[1].Col != 5 {
		t.Fatalf("multiple matches: got %+v", multi)
	}
}

func TestSitesForRegexInTokenUTF16(t *testing.T) {
	// `é` is one UTF-16 unit but two bytes, so the byte and UTF-16 offsets of the
	// match diverge — the reported column must be the UTF-16 one.
	lines := []string{`s := "café_TODO"`}
	tok := SemanticToken{Line: 0, StartChar: 5, Length: 11, TypeName: "string"}
	sites := SitesForRegexInToken("/f.go", tok, lines, regexp.MustCompile(`TODO`))
	if len(sites) != 1 || sites[0].Col != 12 {
		t.Fatalf("utf-16 match: got %+v, want one site at col 12", sites)
	}
}

func TestTokenTextMultiline(t *testing.T) {
	lines := []string{`/* line one`, `   line two */`}
	tok := SemanticToken{Line: 0, StartChar: 0, Length: 26, TypeName: "comment"}
	got := TokenText(tok, lines)
	want := "/* line one\n   line two */"
	if got != want {
		t.Fatalf("TokenText = %q, want %q", got, want)
	}

	// A match on the second physical line reports that line's real position.
	sites := SitesForRegexInToken("/f.go", tok, lines, regexp.MustCompile(`two`))
	if len(sites) != 1 || sites[0].Line != 2 || sites[0].Col != 9 {
		t.Fatalf("multiline match: got %+v, want one site at 2:9", sites)
	}
}
