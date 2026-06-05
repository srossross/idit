// Package treesitter classifies source spans by parsing files with tree-sitter,
// entirely in-process. Unlike the LSP-backed commands it needs no language
// server: grammars are compiled into the binary and a file is parsed directly. It
// backs `idit find --kind …`, restricting regex matches to string literals,
// comments, or symbol-definition names (functions, types, …).
package treesitter

import (
	"maps"
	"regexp"
	"sort"
	"unsafe"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"

	"github.com/srossross/clidit/src/lsputil"
)

// Canonical kinds a search can target. Each maps to a per-grammar query whose
// captured nodes are the spans the regex is matched against — the literal/comment
// text for string/comment, or the declared name for symbol kinds.
const (
	KindString    = "string"
	KindComment   = "comment"
	KindFunction  = "function"
	KindMethod    = "method"
	KindClass     = "class"
	KindInterface = "interface"
	KindType      = "type"
	KindVariable  = "variable"
	KindConst     = "const"
)

// canonicalKinds is the set of recognized kinds (not every grammar supports
// every one).
var canonicalKinds = map[string]struct{}{
	KindString: {}, KindComment: {}, KindFunction: {}, KindMethod: {},
	KindClass: {}, KindInterface: {}, KindType: {}, KindVariable: {}, KindConst: {},
}

// kindAliases maps user-facing shorthands to a canonical kind.
var kindAliases = map[string]string{
	"str": KindString, "strings": KindString,
	"comments": KindComment, "doc": KindComment,
	"func": KindFunction, "fn": KindFunction, "functions": KindFunction,
	"methods":   KindMethod,
	"cls":       KindClass,
	"iface":     KindInterface,
	"types":     KindType,
	"var":       KindVariable,
	"variables": KindVariable,
	"constant":  KindConst,
	"constants": KindConst,
}

// CanonicalKind resolves an alias or canonical kind name, reporting whether it is
// recognized.
func CanonicalKind(s string) (string, bool) {
	if _, ok := canonicalKinds[s]; ok {
		return s, true
	}
	if c, ok := kindAliases[s]; ok {
		return c, true
	}
	return "", false
}

// Grammar is a loaded tree-sitter language plus the precompiled queries that
// capture each supported kind's nodes.
type Grammar struct {
	lang    *sitter.Language
	queries map[string]*sitter.Query // kind -> query (find)
	locate  []locatePattern          // declaration queries for `idit locate`
}

// registry maps a lowercased file extension (with dot) to its grammar.
var registry map[string]*Grammar

func init() {
	goQueries := map[string]string{
		KindString:   "(interpreted_string_literal) @x (raw_string_literal) @x",
		KindComment:  "(comment) @x",
		KindFunction: "(function_declaration name: (identifier) @x)",
		KindMethod:   "(method_declaration name: (field_identifier) @x)",
		KindType:     "(type_spec name: (type_identifier) @x)",
		KindVariable: "(var_spec name: (identifier) @x)",
		KindConst:    "(const_spec name: (identifier) @x)",
	}

	// JavaScript and TypeScript share most node names (TS's grammar extends JS's).
	jsQueries := map[string]string{
		KindString:  "(string) @x (template_string) @x",
		KindComment: "(comment) @x",
		KindFunction: "(function_declaration name: (identifier) @x) " +
			"(function_expression name: (identifier) @x) " +
			"(generator_function_declaration name: (identifier) @x)",
		KindMethod:   "(method_definition name: (property_identifier) @x)",
		KindClass:    "(class_declaration name: (identifier) @x) (class name: (identifier) @x)",
		KindVariable: "(variable_declarator name: (identifier) @x)",
	}
	tsQueries := map[string]string{}
	maps.Copy(tsQueries, jsQueries)
	tsQueries[KindFunction] = jsQueries[KindFunction] + " (function_signature name: (identifier) @x)"
	tsQueries[KindMethod] = jsQueries[KindMethod] +
		" (method_signature name: (property_identifier) @x)" +
		" (abstract_method_signature name: (property_identifier) @x)"
	// In TypeScript a class/interface name is a type_identifier, not an identifier.
	tsQueries[KindClass] = "(class_declaration name: (type_identifier) @x) " +
		"(class name: (type_identifier) @x) " +
		"(abstract_class_declaration name: (type_identifier) @x)"
	tsQueries[KindInterface] = "(interface_declaration name: (type_identifier) @x)"
	tsQueries[KindType] = "(type_alias_declaration name: (type_identifier) @x)"

	goGrammar := newGrammar(tree_sitter_go.Language(), goQueries)
	jsGrammar := newGrammar(tree_sitter_javascript.Language(), jsQueries)
	tsGrammar := newGrammar(tree_sitter_typescript.LanguageTypescript(), tsQueries)
	tsxGrammar := newGrammar(tree_sitter_typescript.LanguageTSX(), tsQueries)

	goGrammar.locate = compileLocate(goGrammar.lang, goLocateSpecs)
	jsGrammar.locate = compileLocate(jsGrammar.lang, jsLocateSpecs)
	tsGrammar.locate = compileLocate(tsGrammar.lang, tsLocateSpecs)
	tsxGrammar.locate = compileLocate(tsxGrammar.lang, tsLocateSpecs)

	registry = map[string]*Grammar{
		".go":  goGrammar,
		".js":  jsGrammar,
		".mjs": jsGrammar,
		".cjs": jsGrammar,
		".jsx": jsGrammar,
		".ts":  tsGrammar,
		".mts": tsGrammar,
		".cts": tsGrammar,
		".tsx": tsxGrammar,
	}
}

// newGrammar wraps a raw grammar pointer and compiles its kind queries. A query
// that references node types the grammar lacks simply disables that kind rather
// than panicking — the registry test guards the kinds we intend to support, so a
// real authoring regression surfaces in tests, never in production.
func newGrammar(raw unsafe.Pointer, queries map[string]string) *Grammar {
	lang := sitter.NewLanguage(raw)
	g := &Grammar{lang: lang, queries: map[string]*sitter.Query{}}
	for kind, src := range queries {
		if q, err := sitter.NewQuery(lang, src); err == nil {
			g.queries[kind] = q
		}
	}
	return g
}

// ForExt returns the grammar handling ext (".go", ".ts", …), or nil if none is
// bundled for it.
func ForExt(ext string) *Grammar {
	return registry[ext]
}

// HasExt reports whether a grammar is bundled for the file's extension.
func HasExt(ext string) bool {
	_, ok := registry[ext]
	return ok
}

// Extensions returns the file extensions with a bundled grammar, sorted.
func Extensions() []string {
	exts := make([]string, 0, len(registry))
	for ext := range registry {
		exts = append(exts, ext)
	}
	sort.Strings(exts)
	return exts
}

// Search parses src and returns a Site for every match of re inside a node of any
// of the given kinds. Each Site points at the exact match start (1-based
// line:col, columns in UTF-16 units to match idit's other output) and carries the
// enclosing node's span as Range. Kinds this grammar doesn't support are skipped.
func (g *Grammar) Search(file string, src []byte, kinds []string, re *regexp.Regexp) []lsputil.Site {
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(g.lang); err != nil {
		return nil
	}
	tree := parser.Parse(src, nil)
	if tree == nil {
		return nil
	}
	defer tree.Close()
	root := tree.RootNode()
	pm := newPosMapper(src)

	var sites []lsputil.Site
	for _, kind := range kinds {
		query := g.queries[kind]
		if query == nil {
			continue
		}
		qc := sitter.NewQueryCursor()
		matches := qc.Matches(query, root, src)
		for {
			m := matches.Next()
			if m == nil {
				break
			}
			for _, capture := range m.Captures {
				node := capture.Node
				//nolint:gosec // byte offsets are bounded by the file length, itself an int
				start, end := int(node.StartByte()), int(node.EndByte())
				sl, sc := pm.at(start)
				el, ec := pm.at(end)
				span := lsputil.Span{StartLine: sl, StartCol: sc, EndLine: el, EndCol: ec}
				for _, loc := range re.FindAllIndex(src[start:end], -1) {
					line, col := pm.at(start + loc[0])
					sites = append(sites, lsputil.Site{File: file, Line: line, Col: col, Range: span})
				}
			}
		}
		qc.Close()
	}
	return sites
}

// posMapper converts a byte offset in a file to a 1-based line and 1-based UTF-16
// column.
type posMapper struct {
	src    []byte
	starts []int // byte offset of each line's first byte
}

func newPosMapper(src []byte) *posMapper {
	starts := []int{0}
	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return &posMapper{src: src, starts: starts}
}

func (p *posMapper) at(byteOff int) (line, col int) {
	li := max(sort.Search(len(p.starts), func(i int) bool { return p.starts[i] > byteOff })-1, 0)
	lineStart := p.starts[li]
	if byteOff > len(p.src) {
		byteOff = len(p.src)
	}
	col = lsputil.UTF16Len(string(p.src[lineStart:byteOff])) + 1
	return li + 1, col
}
