package treesitter

import (
	"sort"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/srossross/clidit/src/lsputil"
)

// Locate resolves a dotted symbol path (e.g. main.x) within a single file by
// walking the syntax tree scope by scope: each segment is matched against the
// declarations inside the previous segment's subtree. Unlike the find kinds, the
// locate queries also capture function-local declarations and parameters, since a
// path like main.x usually targets a local.

// kindQuery pairs a kind label with the tree-sitter query that captures that
// declaration's name node as @x.
type kindQuery struct{ kind, query string }

// locatePattern is a compiled kindQuery.
type locatePattern struct {
	kind string
	q    *sitter.Query
}

// LocateMatch is one resolved declaration name occurrence, in 1-based UTF-16
// coordinates. Range is the enclosing declaration's full span (used by range
// commands like extract).
type LocateMatch struct {
	Line, Col int
	Kind      string
	Range     lsputil.Span
}

// LocateOutcome reports the matches for the segment at index Segment — the first
// segment that is unresolved (no matches) or ambiguous (>1), or the final segment
// on success (exactly one match).
type LocateOutcome struct {
	Segment int
	Matches []LocateMatch
}

// candidate is a declaration found during resolution: its name node (for the
// reported position) and the node to descend into for the next segment.
type candidate struct {
	name    sitter.Node
	descent *sitter.Node
	kind    string
}

var goLocateSpecs = []kindQuery{
	{KindFunction, "(function_declaration name: (identifier) @x)"},
	{KindMethod, "(method_declaration name: (field_identifier) @x)"},
	{KindType, "(type_spec name: (type_identifier) @x)"},
	{"field", "(field_declaration name: (field_identifier) @x)"},
	{KindConst, "(const_spec name: (identifier) @x)"},
	{KindVariable, "(var_spec name: (identifier) @x)"},
	{KindVariable, "(short_var_declaration left: (expression_list (identifier) @x))"},
	{"parameter", "(parameter_declaration name: (identifier) @x)"},
}

var jsLocateSpecs = []kindQuery{
	{KindFunction, "(function_declaration name: (identifier) @x)"},
	{KindFunction, "(function_expression name: (identifier) @x)"},
	{KindFunction, "(generator_function_declaration name: (identifier) @x)"},
	{KindMethod, "(method_definition name: (property_identifier) @x)"},
	{KindClass, "(class_declaration name: (identifier) @x)"},
	{KindClass, "(class name: (identifier) @x)"},
	{KindVariable, "(variable_declarator name: (identifier) @x)"},
	{"parameter", "(formal_parameters (identifier) @x)"},
}

var tsLocateSpecs = []kindQuery{
	{KindFunction, "(function_declaration name: (identifier) @x)"},
	{KindFunction, "(function_expression name: (identifier) @x)"},
	{KindFunction, "(generator_function_declaration name: (identifier) @x)"},
	{KindFunction, "(function_signature name: (identifier) @x)"},
	{KindMethod, "(method_definition name: (property_identifier) @x)"},
	{KindMethod, "(method_signature name: (property_identifier) @x)"},
	{KindClass, "(class_declaration name: (type_identifier) @x)"},
	{KindClass, "(abstract_class_declaration name: (type_identifier) @x)"},
	{KindInterface, "(interface_declaration name: (type_identifier) @x)"},
	{KindType, "(type_alias_declaration name: (type_identifier) @x)"},
	{KindVariable, "(variable_declarator name: (identifier) @x)"},
	{"parameter", "(required_parameter pattern: (identifier) @x)"},
	{"parameter", "(optional_parameter pattern: (identifier) @x)"},
}

// compileLocate compiles the locate specs, skipping any whose node types the
// grammar lacks (the registry test guards the ones we rely on).
func compileLocate(lang *sitter.Language, specs []kindQuery) []locatePattern {
	var out []locatePattern
	for _, s := range specs {
		if q, err := sitter.NewQuery(lang, s.query); err == nil {
			out = append(out, locatePattern{kind: s.kind, q: q})
		}
	}
	return out
}

// Locate resolves segments within src. See LocateOutcome for the result shape.
func (g *Grammar) Locate(src []byte, segments []string) LocateOutcome {
	if len(segments) == 0 {
		return LocateOutcome{Segment: 0}
	}
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(g.lang); err != nil {
		return LocateOutcome{Segment: 0}
	}
	tree := parser.Parse(src, nil)
	if tree == nil {
		return LocateOutcome{Segment: 0}
	}
	defer tree.Close()

	pm := newPosMapper(src)
	scope := tree.RootNode()
	last := len(segments) - 1
	for i, seg := range segments {
		cands := g.declsNamed(scope, src, seg)
		// Stop and report when the segment doesn't resolve uniquely, or when it's
		// the final segment (success is exactly one match here).
		if len(cands) != 1 || i == last {
			return LocateOutcome{Segment: i, Matches: toLocateMatches(cands, pm)}
		}
		scope = cands[0].descent
	}
	return LocateOutcome{Segment: last}
}

// declsNamed returns the declarations named name within scope's subtree, in
// document order, deduped by position.
func (g *Grammar) declsNamed(scope *sitter.Node, src []byte, name string) []candidate {
	seen := map[uint]bool{}
	var out []candidate
	for _, lp := range g.locate {
		qc := sitter.NewQueryCursor()
		matches := qc.Matches(lp.q, scope, src)
		for {
			m := matches.Next()
			if m == nil {
				break
			}
			for _, capture := range m.Captures {
				n := capture.Node
				if n.Utf8Text(src) != name {
					continue
				}
				sb := n.StartByte()
				if seen[sb] {
					continue
				}
				seen[sb] = true
				descent := n.Parent()
				if descent == nil {
					descent = scope
				}
				out = append(out, candidate{name: n, descent: descent, kind: lp.kind})
			}
		}
		qc.Close()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name.StartByte() < out[j].name.StartByte() })
	return out
}

func toLocateMatches(cands []candidate, pm *posMapper) []LocateMatch {
	out := make([]LocateMatch, len(cands))
	for i, c := range cands {
		//nolint:gosec // byte offset is bounded by the file length, itself an int
		line, col := pm.at(int(c.name.StartByte()))
		m := LocateMatch{Line: line, Col: col, Kind: c.kind}
		if c.descent != nil {
			//nolint:gosec // byte offsets are bounded by the file length, itself an int
			sl, sc := pm.at(int(c.descent.StartByte()))
			//nolint:gosec // byte offsets are bounded by the file length, itself an int
			el, ec := pm.at(int(c.descent.EndByte()))
			m.Range = lsputil.Span{StartLine: sl, StartCol: sc, EndLine: el, EndCol: ec}
		}
		out[i] = m
	}
	return out
}
