package lsputil

import (
	"encoding/json"
	"testing"
)

func TestCompletionItemsArrayAndList(t *testing.T) {
	arr := CompletionItems(json.RawMessage(`[{"label":"a"},{"label":"b"}]`))
	if len(arr) != 2 {
		t.Fatalf("array: want 2, got %d", len(arr))
	}
	list := CompletionItems(json.RawMessage(`{"isIncomplete":true,"items":[{"label":"c"}]}`))
	if len(list) != 1 || list[0].Label != "c" {
		t.Fatalf("list: %+v", list)
	}
	if CompletionItems(json.RawMessage(`null`)) != nil {
		t.Fatal("null should be nil")
	}
}

func TestIsIncomplete(t *testing.T) {
	if !IsIncomplete(json.RawMessage(`{"isIncomplete":true,"items":[]}`)) {
		t.Fatal("want incomplete")
	}
	if IsIncomplete(json.RawMessage(`[{"label":"a"}]`)) {
		t.Fatal("array is never incomplete")
	}
}

func TestToMembersSortsBySortText(t *testing.T) {
	items := []CompletionItem{
		{Label: "zebra", Kind: 5, SortText: "1"},
		{Label: "apple", Kind: 2, SortText: "2"},
	}
	members := ToMembers(items)
	if members[0].Label != "zebra" || members[1].Label != "apple" {
		t.Fatalf("sortText ordering wrong: %+v", members)
	}
	if members[0].Kind != "field" || members[1].Kind != "method" {
		t.Fatalf("kind mapping wrong: %+v", members)
	}
}

func TestHoverToText(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"contents":"plain text"}`, "plain text"},
		{`{"contents":{"language":"ts","value":"const x"}}`, "```ts\nconst x\n```"},
		{`{"contents":{"kind":"markdown","value":"# hi"}}`, "# hi"},
		{`{"contents":["a","b"]}`, "a\nb"},
		{`{"contents":null}`, ""},
	}
	for _, c := range cases {
		if got := HoverToText(json.RawMessage(c.in)); got != c.want {
			t.Errorf("HoverToText(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestToOutlineDocumentSymbolTree(t *testing.T) {
	raw := json.RawMessage(`[{"name":"Foo","kind":5,"range":{"start":{"line":0,"character":0},"end":{"line":9,"character":0}},"selectionRange":{"start":{"line":0,"character":6},"end":{"line":0,"character":9}},"children":[{"name":"bar","kind":6,"range":{"start":{"line":1,"character":2},"end":{"line":1,"character":5}},"selectionRange":{"start":{"line":1,"character":2},"end":{"line":1,"character":5}}}]}]`)
	tree := ToOutline(raw)
	if len(tree) != 1 || tree[0].Name != "Foo" || tree[0].Kind != "class" {
		t.Fatalf("root wrong: %+v", tree)
	}
	if tree[0].Line != 1 || tree[0].Col != 7 {
		t.Fatalf("selectionRange used? %+v", tree[0])
	}
	if len(tree[0].Children) != 1 || tree[0].Children[0].Name != "bar" {
		t.Fatalf("children wrong: %+v", tree[0].Children)
	}
}

func TestToOutlineSymbolInformationFlat(t *testing.T) {
	raw := json.RawMessage(`[{"name":"g","kind":12,"location":{"uri":"file:///a.ts","range":{"start":{"line":2,"character":0},"end":{"line":2,"character":1}}}}]`)
	tree := ToOutline(raw)
	if len(tree) != 1 || tree[0].Kind != "function" || tree[0].Line != 3 {
		t.Fatalf("flat outline wrong: %+v", tree)
	}
}

func TestToCallersWithRanges(t *testing.T) {
	raw := json.RawMessage(`[{"from":{"name":"caller","kind":12,"uri":"file:///c.ts","range":{"start":{"line":0,"character":0},"end":{"line":5,"character":0}},"selectionRange":{"start":{"line":0,"character":9},"end":{"line":0,"character":15}}},"fromRanges":[{"start":{"line":3,"character":4},"end":{"line":3,"character":10}},{"start":{"line":4,"character":4},"end":{"line":4,"character":10}}]}]`)
	callers := ToCallers(raw)
	if len(callers) != 2 {
		t.Fatalf("want 2 call sites, got %d", len(callers))
	}
	if callers[0].Line != 4 || callers[1].Line != 5 || callers[0].Name != "caller" {
		t.Fatalf("caller sites wrong: %+v", callers)
	}
}

func TestToCallersFallbackNoRanges(t *testing.T) {
	raw := json.RawMessage(`[{"from":{"name":"c","kind":12,"uri":"file:///c.ts","range":{"start":{"line":0,"character":0},"end":{"line":1,"character":0}},"selectionRange":{"start":{"line":0,"character":9},"end":{"line":0,"character":10}}},"fromRanges":[]}]`)
	callers := ToCallers(raw)
	if len(callers) != 1 || callers[0].Line != 1 || callers[0].Col != 10 {
		t.Fatalf("fallback wrong: %+v", callers)
	}
}

func TestNormalizeDiagnostics(t *testing.T) {
	diags := []Diagnostic{{
		Range:    Range{Start: Position{Line: 4, Character: 2}, End: Position{Line: 4, Character: 8}},
		Severity: 1, Code: json.RawMessage(`2304`), Source: "ts", Message: "Cannot find name",
	}}
	out := NormalizeDiagnostics(diags)
	if out[0].Line != 5 || out[0].Col != 3 || out[0].Severity != "error" {
		t.Fatalf("normalize wrong: %+v", out[0])
	}
	if out[0].CodeString() != "2304" {
		t.Fatalf("code string wrong: %q", out[0].CodeString())
	}
}

func TestApplyTextEditsBottomToTop(t *testing.T) {
	content := "line0\nline1\nline2\n"
	edits := []TextEdit{
		{Range: Range{Start: Position{0, 0}, End: Position{0, 5}}, NewText: "AAAA"},
		{Range: Range{Start: Position{2, 0}, End: Position{2, 5}}, NewText: "ZZ"},
	}
	got := ApplyTextEdits(content, edits)
	want := "AAAA\nline1\nZZ\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestApplyTextEditsInsertion(t *testing.T) {
	got := ApplyTextEdits("abc", []TextEdit{
		{Range: Range{Start: Position{0, 1}, End: Position{0, 1}}, NewText: "X"},
	})
	if got != "aXbc" {
		t.Fatalf("got %q", got)
	}
}

func TestPlanWorkspaceEditDocumentChanges(t *testing.T) {
	edit := WorkspaceEdit{DocumentChanges: []json.RawMessage{
		json.RawMessage(`{"textDocument":{"uri":"file:///a.ts"},"edits":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"newText":"x"}]}`),
		json.RawMessage(`{"kind":"rename","oldUri":"file:///old.ts","newUri":"file:///new.ts"}`),
		json.RawMessage(`{"kind":"create","uri":"file:///c.ts"}`),
		json.RawMessage(`{"kind":"delete","uri":"file:///d.ts"}`),
	}}
	ops := PlanWorkspaceEdit(edit)
	if len(ops) != 4 {
		t.Fatalf("want 4 ops, got %d", len(ops))
	}
	if ops[0].Kind != OpEdit || ops[0].File != "/a.ts" || len(ops[0].Edits) != 1 {
		t.Fatalf("edit op wrong: %+v", ops[0])
	}
	if ops[1].Kind != OpRename || ops[1].From != "/old.ts" || ops[1].To != "/new.ts" {
		t.Fatalf("rename op wrong: %+v", ops[1])
	}
	if ops[2].Kind != OpCreate || ops[3].Kind != OpDelete {
		t.Fatalf("create/delete wrong: %+v %+v", ops[2], ops[3])
	}
}

func TestPlanWorkspaceEditChangesMap(t *testing.T) {
	edit := WorkspaceEdit{Changes: map[string][]TextEdit{
		"file:///a.ts": {{Range: Range{}, NewText: "x"}},
	}}
	ops := PlanWorkspaceEdit(edit)
	if len(ops) != 1 || ops[0].Kind != OpEdit || ops[0].File != "/a.ts" {
		t.Fatalf("changes map wrong: %+v", ops)
	}
}
