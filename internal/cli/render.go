package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/srossross/clidit/internal/lsputil"
)

// How many lines of a definition to show before eliding the middle.
const previewFullLimit = 12
const previewHead = 8

// lineCache caches a file's split lines so repeated lookups read disk once.
type lineCache map[string][]string

// lineAt reads a single 1-based line from a file, caching the split per file.
func (c lineCache) lineAt(file string, line int) string {
	lines, ok := c[file]
	if !ok {
		data, err := os.ReadFile(file)
		if err != nil {
			lines = nil
		} else {
			lines = strings.Split(string(data), "\n")
		}
		c[file] = lines
	}
	if line-1 < 0 || line-1 >= len(lines) {
		return ""
	}
	return strings.TrimRight(lines[line-1], " \t\r")
}

// renderPreview reads a declaration's source and renders it ripgrep-style: a
// right-aligned `N:` line-number gutter on every line, with `--` marking an
// elided body.
func renderPreview(site lsputil.Site) string {
	data, err := os.ReadFile(site.File)
	if err != nil {
		return "" // file unreadable — just skip the preview
	}
	lines := strings.Split(string(data), "\n")

	startLine := site.Range.StartLine
	endLine := site.Range.EndLine
	if endLine < startLine {
		return ""
	}

	type row struct {
		n   int
		gap bool
	}
	var rows []row
	total := endLine - startLine + 1
	if total <= previewFullLimit {
		for n := startLine; n <= endLine; n++ {
			rows = append(rows, row{n: n})
		}
	} else {
		for n := startLine; n < startLine+previewHead; n++ {
			rows = append(rows, row{n: n})
		}
		rows = append(rows, row{gap: true})
		rows = append(rows, row{n: endLine})
	}

	width := len(fmt.Sprintf("%d", endLine))
	var b strings.Builder
	for i, r := range rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		if r.gap {
			b.WriteString("--")
			continue
		}
		text := ""
		if r.n-1 >= 0 && r.n-1 < len(lines) {
			text = lines[r.n-1]
		}
		fmt.Fprintf(&b, "%*d:%s", width, r.n, text)
	}
	return b.String()
}

// stripCodeFences drops markdown code-fence lines so hover reads cleanly in a
// terminal.
func stripCodeFences(markdown string) string {
	lines := strings.Split(markdown, "\n")
	kept := lines[:0]
	for _, l := range lines {
		if !strings.HasPrefix(l, "```") {
			kept = append(kept, l)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// printOutline writes an outline tree with two-space indentation per depth.
func printOutline(out *strings.Builder, file string, nodes []lsputil.OutlineNode, depth int) {
	for _, node := range nodes {
		indent := strings.Repeat("  ", depth)
		fmt.Fprintf(out, "%s:%d:%d  %s%s %s\n", file, node.Line, node.Col, indent, node.Kind, node.Name)
		printOutline(out, file, node.Children, depth+1)
	}
}

// flattenOutline returns all nodes of an outline tree in pre-order.
func flattenOutline(nodes []lsputil.OutlineNode) []lsputil.OutlineNode {
	var out []lsputil.OutlineNode
	for _, node := range nodes {
		out = append(out, node)
		out = append(out, flattenOutline(node.Children)...)
	}
	return out
}
