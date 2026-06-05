package treesitter

import (
	"regexp"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// searchLineCols runs a search and flattens the sites to (line, col) pairs for
// compact assertions.
func searchLineCols(t *testing.T, ext, src string, kinds []string, pattern string) [][2]int {
	t.Helper()
	g := ForExt(ext)
	if g == nil {
		t.Fatalf("no grammar for %s", ext)
	}
	sites := g.Search("f"+ext, []byte(src), kinds, regexp.MustCompile(pattern))
	out := make([][2]int, len(sites))
	for i, s := range sites {
		out[i] = [2]int{s.Line, s.Col}
	}
	return out
}

func wantPairs(t *testing.T, got [][2]int, want ...[2]int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestSearchGoStrings(t *testing.T) {
	src := "package main\n\n// a TODO comment\nvar x = \"hello TODO world\"\nvar y = `raw TODO`\n"
	got := searchLineCols(t, ".go", src, []string{KindString}, "TODO")
	wantPairs(t, got, [2]int{4, 16}, [2]int{5, 14})
}

func TestSearchGoComments(t *testing.T) {
	src := "package main\n\n// a TODO comment\nvar x = \"hello TODO world\"\n"
	got := searchLineCols(t, ".go", src, []string{KindComment}, "TODO")
	wantPairs(t, got, [2]int{3, 6})
}

func TestSearchGoUTF16Column(t *testing.T) {
	// `é` is one UTF-16 unit but two bytes; the reported column is the UTF-16 one.
	src := "package main\n\nvar s = \"café TODO\"\n"
	got := searchLineCols(t, ".go", src, []string{KindString}, "TODO")
	wantPairs(t, got, [2]int{3, 15})
}

func TestSearchGoFunctionAndType(t *testing.T) {
	src := "package main\n\nfunc doThing() {}\nfunc other() {}\ntype Thing struct{}\n"
	// The match is reported at its offset within the name span: "Thing" sits at
	// col 8 inside "doThing" (func doThing), and col 6 in "type Thing".
	fns := searchLineCols(t, ".go", src, []string{KindFunction}, "Thing")
	wantPairs(t, fns, [2]int{3, 8})
	types := searchLineCols(t, ".go", src, []string{KindType}, "Thing")
	wantPairs(t, types, [2]int{5, 6})
	// Multiple kinds union; "o" hits inside doThing (col 7) and other (col 6).
	multi := searchLineCols(t, ".go", src, []string{KindFunction, KindType}, "o")
	wantPairs(t, multi, [2]int{3, 7}, [2]int{4, 6})
}

func TestSearchTypeScriptKinds(t *testing.T) {
	src := "// TODO comment\nconst x = \"TODO str\";\nfunction doIt() {}\nclass Widget {}\ninterface Shape {}\ntype Id = string;\n"
	wantPairs(t, searchLineCols(t, ".ts", src, []string{KindString}, "TODO"), [2]int{2, 12})
	wantPairs(t, searchLineCols(t, ".ts", src, []string{KindComment}, "TODO"), [2]int{1, 4})
	wantPairs(t, searchLineCols(t, ".ts", src, []string{KindFunction}, "doIt"), [2]int{3, 10})
	wantPairs(t, searchLineCols(t, ".ts", src, []string{KindClass}, "Widget"), [2]int{4, 7})
	wantPairs(t, searchLineCols(t, ".ts", src, []string{KindInterface}, "Shape"), [2]int{5, 11})
	wantPairs(t, searchLineCols(t, ".ts", src, []string{KindType}, "Id"), [2]int{6, 6})
}

func TestSearchCKinds(t *testing.T) {
	src := "// TODO comment\n" + // line 1
		"int doThing(int n) { return n; }\n" + // line 2
		"struct Widget { int x; };\n" + // line 3
		"const char *s = \"TODO str\";\n" + // line 4
		"int count = 0;\n" // line 5
	wantPairs(t, searchLineCols(t, ".c", src, []string{KindComment}, "TODO"), [2]int{1, 4})
	wantPairs(t, searchLineCols(t, ".c", src, []string{KindFunction}, "doThing"), [2]int{2, 5})
	wantPairs(t, searchLineCols(t, ".c", src, []string{KindType}, "Widget"), [2]int{3, 8})
	wantPairs(t, searchLineCols(t, ".c", src, []string{KindString}, "TODO"), [2]int{4, 18})
	wantPairs(t, searchLineCols(t, ".c", src, []string{KindVariable}, "count"), [2]int{5, 5})
}

func TestSearchCppKinds(t *testing.T) {
	src := "class Widget {\n" + // line 1
		"  void doThing() {}\n" + // line 2 (in-class definition with body)
		"  int decl();\n" + // line 3 (in-class declaration, no body)
		"};\n" +
		"void Widget::outOfLine() {}\n" // line 5 (out-of-line definition)
	wantPairs(t, searchLineCols(t, ".cpp", src, []string{KindClass}, "Widget"), [2]int{1, 7})
	wantPairs(t, searchLineCols(t, ".cpp", src, []string{KindMethod}, "doThing"), [2]int{2, 8})
	wantPairs(t, searchLineCols(t, ".cpp", src, []string{KindMethod}, "decl"), [2]int{3, 7})
	wantPairs(t, searchLineCols(t, ".cpp", src, []string{KindMethod}, "outOfLine"), [2]int{5, 14})
}

func TestSearchPythonKinds(t *testing.T) {
	src := "# TODO comment\n" + // line 1
		"x = \"TODO str\"\n" + // line 2
		"def do_it(): pass\n" + // line 3
		"class Widget: pass\n" // line 4
	wantPairs(t, searchLineCols(t, ".py", src, []string{KindComment}, "TODO"), [2]int{1, 3})
	wantPairs(t, searchLineCols(t, ".py", src, []string{KindString}, "TODO"), [2]int{2, 6})
	wantPairs(t, searchLineCols(t, ".py", src, []string{KindFunction}, "do_it"), [2]int{3, 5})
	wantPairs(t, searchLineCols(t, ".py", src, []string{KindClass}, "Widget"), [2]int{4, 7})
	wantPairs(t, searchLineCols(t, ".py", src, []string{KindVariable}, "x"), [2]int{2, 1})
}

func TestSearchSQLKinds(t *testing.T) {
	src := "-- TODO comment\n" + // line 1
		"/* TODO block */\n" + // line 2 (marginalia)
		"CREATE TABLE widgets (\n" + // line 3
		"  gizmo_id int,\n" + // line 4
		"  label varchar\n" + // line 5
		");\n" +
		"INSERT INTO widgets VALUES (1, 'TODO str');\n" // line 7
	wantPairs(t, searchLineCols(t, ".sql", src, []string{KindComment}, "TODO"), [2]int{1, 4}, [2]int{2, 4})
	wantPairs(t, searchLineCols(t, ".sql", src, []string{KindType}, "widgets"), [2]int{3, 14})
	wantPairs(t, searchLineCols(t, ".sql", src, []string{KindVariable}, "gizmo_id"), [2]int{4, 3})
	wantPairs(t, searchLineCols(t, ".sql", src, []string{KindString}, "TODO"), [2]int{7, 33})
}

func TestLocatePython(t *testing.T) {
	src := "def handle(arg):\n    y = arg\n    return y\n"
	out := ForExt(".py").Locate([]byte(src), []string{"handle", "y"})
	if len(out.Matches) != 1 || out.Matches[0].Line != 2 {
		t.Fatalf("want single match at line 2, got %+v", out)
	}
	// A parameter resolves too.
	param := ForExt(".py").Locate([]byte(src), []string{"handle", "arg"})
	if len(param.Matches) != 1 || param.Matches[0].Line != 1 {
		t.Fatalf("want parameter match at line 1, got %+v", param)
	}
}

func TestLocateC(t *testing.T) {
	// A top-level function and its parameter resolve; body locals do not (the
	// function name nests under function_declarator, a sibling of the body).
	src := "int doThing(int n) {\n\tint y = n;\n\treturn y;\n}\n"
	fn := ForExt(".c").Locate([]byte(src), []string{"doThing"})
	if len(fn.Matches) != 1 || fn.Matches[0].Line != 1 {
		t.Fatalf("want single function match at line 1, got %+v", fn)
	}
	param := ForExt(".c").Locate([]byte(src), []string{"doThing", "n"})
	if len(param.Matches) != 1 || param.Matches[0].Line != 1 {
		t.Fatalf("want parameter match at line 1, got %+v", param)
	}
}

func TestCanonicalKind(t *testing.T) {
	cases := map[string]string{
		"function": KindFunction, "func": KindFunction, "fn": KindFunction,
		"str": KindString, "var": KindVariable, "iface": KindInterface,
	}
	for in, want := range cases {
		if got, ok := CanonicalKind(in); !ok || got != want {
			t.Errorf("CanonicalKind(%q) = %q,%v; want %q", in, got, ok, want)
		}
	}
	if _, ok := CanonicalKind("nope"); ok {
		t.Error("CanonicalKind(nope) should be unknown")
	}
}

func TestNoGrammar(t *testing.T) {
	if ForExt(".rs") != nil || HasExt(".rs") {
		t.Fatal("unexpected grammar for .rs")
	}
}

func TestLocateGoSingle(t *testing.T) {
	src := "package main\n\nfunc main() {\n\tx := 1\n\t_ = x\n}\n"
	out := ForExt(".go").Locate([]byte(src), []string{"main", "x"})
	if len(out.Matches) != 1 || out.Matches[0].Line != 4 {
		t.Fatalf("want single match at line 4, got %+v", out)
	}
}

func TestLocateGoAmbiguous(t *testing.T) {
	// x is declared twice within main (shadowed in a block) → ambiguous.
	src := "package main\n\nfunc main() {\n\tx := 1\n\tif true {\n\t\tx := 2\n\t\t_ = x\n\t}\n\t_ = x\n}\n"
	out := ForExt(".go").Locate([]byte(src), []string{"main", "x"})
	if len(out.Matches) != 2 || out.Segment != 1 {
		t.Fatalf("want 2 candidates at segment 1, got %+v", out)
	}
	if out.Matches[0].Line != 4 || out.Matches[1].Line != 6 {
		t.Fatalf("want candidates on lines 4 and 6, got %+v", out.Matches)
	}
}

func TestLocateNotFound(t *testing.T) {
	src := "package main\n\nfunc main() {}\n"
	out := ForExt(".go").Locate([]byte(src), []string{"main", "zzz"})
	if len(out.Matches) != 0 || out.Segment != 1 {
		t.Fatalf("want no match at segment 1, got %+v", out)
	}
}

func TestLocateTypeScript(t *testing.T) {
	src := "function handle(arg: string) {\n  const y = arg;\n  return y;\n}\n"
	out := ForExt(".ts").Locate([]byte(src), []string{"handle", "y"})
	if len(out.Matches) != 1 || out.Matches[0].Line != 2 {
		t.Fatalf("want single match at line 2, got %+v", out)
	}
	// A parameter resolves too.
	param := ForExt(".ts").Locate([]byte(src), []string{"handle", "arg"})
	if len(param.Matches) != 1 || param.Matches[0].Line != 1 {
		t.Fatalf("want parameter match at line 1, got %+v", param)
	}
}

// TestRegistryKindsCompile guards the runtime↔grammar ABI and the per-grammar
// query authoring: every grammar must parse, and each must support the kinds we
// intend it to. A bad node name (or an incompatible ABI bump) fails here loudly
// instead of silently disabling a kind in production.
func TestRegistryKindsCompile(t *testing.T) {
	want := map[string][]string{
		".go":  {KindString, KindComment, KindFunction, KindMethod, KindType, KindVariable, KindConst},
		".js":  {KindString, KindComment, KindFunction, KindMethod, KindClass, KindVariable},
		".ts":  {KindString, KindComment, KindFunction, KindMethod, KindClass, KindInterface, KindType, KindVariable},
		".c":   {KindString, KindComment, KindFunction, KindType, KindVariable},
		".cpp": {KindString, KindComment, KindFunction, KindClass, KindMethod, KindType, KindVariable},
		".py":  {KindString, KindComment, KindFunction, KindClass, KindVariable},
		".sql": {KindString, KindComment, KindType, KindVariable},
	}
	for ext, kinds := range want {
		g := ForExt(ext)
		if g == nil {
			t.Errorf("%s: no grammar", ext)
			continue
		}
		parser := sitter.NewParser()
		if err := parser.SetLanguage(g.lang); err != nil {
			t.Errorf("%s: incompatible grammar ABI: %v", ext, err)
		}
		parser.Close()
		for _, k := range kinds {
			if g.queries[k] == nil {
				t.Errorf("%s: missing query for kind %q", ext, k)
			}
		}
	}
}
