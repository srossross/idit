package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srossross/clidit/internal/lsputil"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f.ts")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRenderPreviewShort(t *testing.T) {
	file := writeTemp(t, "line1\nline2\nline3\nline4\n")
	site := lsputil.Site{File: file, Range: lsputil.Span{StartLine: 1, EndLine: 3}}
	got := renderPreview(site)
	want := "1:line1\n2:line2\n3:line3"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderPreviewElided(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 20; i++ {
		sb.WriteString("l")
		sb.WriteString(strings.Repeat("x", i))
		sb.WriteByte('\n')
	}
	file := writeTemp(t, sb.String())
	site := lsputil.Site{File: file, Range: lsputil.Span{StartLine: 1, EndLine: 20}}
	got := renderPreview(site)
	lines := strings.Split(got, "\n")
	// 8 head lines + gap + 1 tail = 10 rows.
	if len(lines) != previewHead+2 {
		t.Fatalf("want %d rows, got %d:\n%s", previewHead+2, len(lines), got)
	}
	if lines[previewHead] != "--" {
		t.Fatalf("expected gap marker at row %d, got %q", previewHead, lines[previewHead])
	}
	// Gutter is right-aligned to width of "20" = 2.
	if !strings.HasPrefix(lines[0], " 1:") {
		t.Fatalf("gutter not right-aligned: %q", lines[0])
	}
}

func TestStripCodeFences(t *testing.T) {
	in := "```ts\nconst x = 1\n```\nsome note"
	got := stripCodeFences(in)
	if got != "const x = 1\nsome note" {
		t.Fatalf("got %q", got)
	}
}

func TestPrintOutline(t *testing.T) {
	nodes := []lsputil.OutlineNode{{
		Name: "Foo", Kind: "class", Line: 1, Col: 7,
		Children: []lsputil.OutlineNode{{Name: "bar", Kind: "method", Line: 2, Col: 3}},
	}}
	var sb strings.Builder
	printOutline(&sb, "x.ts", nodes, 0)
	want := "x.ts:1:7  class Foo\nx.ts:2:3    method bar\n"
	if sb.String() != want {
		t.Fatalf("got:\n%q\nwant:\n%q", sb.String(), want)
	}
}

func TestLineAtCache(t *testing.T) {
	file := writeTemp(t, "alpha\nbeta  \ngamma\n")
	c := lineCache{}
	if got := c.lineAt(file, 2); got != "beta" { // trailing spaces trimmed
		t.Fatalf("lineAt = %q", got)
	}
	if got := c.lineAt(file, 99); got != "" {
		t.Fatalf("out of range should be empty, got %q", got)
	}
}
