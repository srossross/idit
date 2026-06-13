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
	tree_sitter_c "github.com/tree-sitter/tree-sitter-c/bindings/go"
	tree_sitter_cpp "github.com/tree-sitter/tree-sitter-cpp/bindings/go"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"

	"github.com/srossross/idit/src/lsputil"
	tree_sitter_sql "github.com/srossross/idit/src/treesitter/sqlgrammar"
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

	// C, and C++ which extends it (shared node names for the C constructs, plus
	// classes/methods). A typedef's name is reached via its declarator, which for a
	// plain `typedef T Name;` is a type_identifier; function-pointer/array typedefs
	// nest deeper and aren't captured.
	cQueries := map[string]string{
		KindString:   "(string_literal) @x (char_literal) @x",
		KindComment:  "(comment) @x",
		KindFunction: "(function_definition declarator: (function_declarator declarator: (identifier) @x))",
		KindType: "(struct_specifier name: (type_identifier) @x) " +
			"(union_specifier name: (type_identifier) @x) " +
			"(enum_specifier name: (type_identifier) @x) " +
			"(type_definition declarator: (type_identifier) @x)",
		KindVariable: "(declaration declarator: (identifier) @x) " +
			"(declaration declarator: (init_declarator declarator: (identifier) @x))",
	}
	cppQueries := map[string]string{}
	maps.Copy(cppQueries, cQueries)
	cppQueries[KindClass] = "(class_specifier name: (type_identifier) @x)"
	// A method name is a field_identifier whether it's an in-class declaration
	// (field_declaration) or an in-class definition with a body (function_definition);
	// an out-of-line definition (void C::m() {}) names it via qualified_identifier.
	cppQueries[KindMethod] = "(field_declaration declarator: (function_declarator declarator: (field_identifier) @x)) " +
		"(function_definition declarator: (function_declarator declarator: (field_identifier) @x)) " +
		"(function_definition declarator: (function_declarator declarator: (qualified_identifier name: (identifier) @x)))"

	// Python has no distinct method node — a method is just a function_definition
	// nested in a class body — so it supports function but not method. The plain
	// function_definition query also matches decorated defs, since tree-sitter
	// matches nodes anywhere in the subtree regardless of the decorator wrapper.
	pyQueries := map[string]string{
		KindString:   "(string) @x",
		KindComment:  "(comment) @x",
		KindFunction: "(function_definition name: (identifier) @x)",
		KindClass:    "(class_definition name: (identifier) @x)",
		KindVariable: "(assignment left: (identifier) @x)",
	}

	// SQL (DerekStride grammar). The grammar has no dedicated string node — string,
	// numeric, boolean and null literals all parse to a single `literal` node — so
	// KindString maps to `literal` and will also match inside numeric literals.
	// Block comments are `marginalia`; line comments are `comment`. Type targets
	// table/view names; variable targets column names in a CREATE TABLE.
	sqlQueries := map[string]string{
		KindString:  "(literal) @x",
		KindComment: "(comment) @x (marginalia) @x",
		KindType: "(create_table (object_reference name: (identifier) @x)) " +
			"(create_view (object_reference name: (identifier) @x))",
		KindVariable: "(column_definition name: (identifier) @x)",
	}

	goGrammar := newGrammar(tree_sitter_go.Language(), goQueries)
	jsGrammar := newGrammar(tree_sitter_javascript.Language(), jsQueries)
	tsGrammar := newGrammar(tree_sitter_typescript.LanguageTypescript(), tsQueries)
	tsxGrammar := newGrammar(tree_sitter_typescript.LanguageTSX(), tsQueries)
	cGrammar := newGrammar(tree_sitter_c.Language(), cQueries)
	cppGrammar := newGrammar(tree_sitter_cpp.Language(), cppQueries)
	pyGrammar := newGrammar(tree_sitter_python.Language(), pyQueries)
	sqlGrammar := newGrammar(tree_sitter_sql.Language(), sqlQueries)

	goGrammar.locate = compileLocate(goGrammar.lang, goLocateSpecs)
	jsGrammar.locate = compileLocate(jsGrammar.lang, jsLocateSpecs)
	tsGrammar.locate = compileLocate(tsGrammar.lang, tsLocateSpecs)
	tsxGrammar.locate = compileLocate(tsxGrammar.lang, tsLocateSpecs)
	cGrammar.locate = compileLocate(cGrammar.lang, cLocateSpecs)
	cppGrammar.locate = compileLocate(cppGrammar.lang, cppLocateSpecs)
	pyGrammar.locate = compileLocate(pyGrammar.lang, pyLocateSpecs)
	sqlGrammar.locate = compileLocate(sqlGrammar.lang, sqlLocateSpecs)

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
		".c":   cGrammar,
		// .h is parsed as C++: a near-superset of C, so it handles both C and C++
		// headers; dedicated extensions pick the precise grammar.
		".h":   cppGrammar,
		".cpp": cppGrammar,
		".cc":  cppGrammar,
		".cxx": cppGrammar,
		".hpp": cppGrammar,
		".hh":  cppGrammar,
		".py":  pyGrammar,
		".pyi": pyGrammar,
		".sql": sqlGrammar,
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

// ReplaceMatch is one regex match inside a kind span: the absolute byte range to
// splice and the replacement text already expanded from the regex's submatches.
// Line/Col (1-based, UTF-16 column) point at the match start for reporting.
type ReplaceMatch struct {
	StartByte, EndByte int
	Line, Col          int
	New                string
}

// SearchReplace finds every match of re inside a node of any of the given kinds
// and builds each match's replacement text from repl via re.Expand ($1/${name}
// supported). It is the write-twin of Search: same parse/query loop, but it
// returns absolute byte ranges to splice instead of report-only Sites. Matches
// are de-duplicated by start byte so a node captured by more than one kind query
// is spliced once. Kinds this grammar doesn't support are skipped.
func (g *Grammar) SearchReplace(src []byte, kinds []string, re *regexp.Regexp, repl string) []ReplaceMatch {
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

	var matches []ReplaceMatch
	seen := map[int]bool{} // start byte -> already emitted
	replBytes := []byte(repl)
	for _, kind := range kinds {
		query := g.queries[kind]
		if query == nil {
			continue
		}
		qc := sitter.NewQueryCursor()
		qm := qc.Matches(query, root, src)
		for {
			m := qm.Next()
			if m == nil {
				break
			}
			for _, capture := range m.Captures {
				node := capture.Node
				//nolint:gosec // byte offsets are bounded by the file length, itself an int
				start, end := int(node.StartByte()), int(node.EndByte())
				for _, loc := range re.FindAllSubmatchIndex(src[start:end], -1) {
					ms, me := start+loc[0], start+loc[1]
					if seen[ms] {
						continue
					}
					seen[ms] = true
					line, col := pm.at(ms)
					newText := re.Expand(nil, replBytes, src[start:end], loc)
					matches = append(matches, ReplaceMatch{
						StartByte: ms, EndByte: me, Line: line, Col: col, New: string(newText),
					})
				}
			}
		}
		qc.Close()
	}
	return matches
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
