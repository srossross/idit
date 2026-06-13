package treesitter

import (
	"regexp"
	"testing"
)

// TestSearchReplaceKindString confirms --kind string rewrites only the string
// literal, leaving the identical token in a comment and an identifier untouched.
func TestSearchReplaceKindString(t *testing.T) {
	src := []byte("package main\n\n// TODO comment\nvar TODO = \"TODO here\"\n")
	g := ForExt(".go")
	got := g.SearchReplace(src, []string{KindString}, regexp.MustCompile("TODO"), "DONE")
	if len(got) != 1 {
		t.Fatalf("want 1 match (string literal only), got %d: %+v", len(got), got)
	}
	m := got[0]
	if old := string(src[m.StartByte:m.EndByte]); old != "TODO" {
		t.Fatalf("matched byte range = %q, want %q", old, "TODO")
	}
	if m.New != "DONE" {
		t.Fatalf("replacement = %q, want %q", m.New, "DONE")
	}
	if m.Line != 4 {
		t.Fatalf("line = %d, want 4", m.Line)
	}
}

// TestSearchReplaceSubmatch confirms $1/${name} expand from the match's
// submatches via re.Expand.
func TestSearchReplaceSubmatch(t *testing.T) {
	src := []byte("var s = \"id_42 here\"\n")
	g := ForExt(".go")
	re := regexp.MustCompile(`id_(\d+)`)
	got := g.SearchReplace(src, []string{KindString}, re, "n$1")
	if len(got) != 1 {
		t.Fatalf("want 1 match, got %d: %+v", len(got), got)
	}
	if got[0].New != "n42" {
		t.Fatalf("replacement = %q, want %q", got[0].New, "n42")
	}
}

// TestSearchReplaceMultiKind confirms a union of kinds emits one match per
// distinct site (deduped by start byte), here a comment hit and a string hit.
func TestSearchReplaceMultiKind(t *testing.T) {
	src := []byte("// TODO comment\nvar s = \"TODO here\"\n")
	g := ForExt(".go")
	got := g.SearchReplace(src, []string{KindString, KindComment}, regexp.MustCompile("TODO"), "X")
	if len(got) != 2 {
		t.Fatalf("want 2 matches (one comment, one string), got %d: %+v", len(got), got)
	}
	starts := map[int]bool{got[0].StartByte: true, got[1].StartByte: true}
	if len(starts) != 2 {
		t.Fatalf("matches share a start byte (not deduped distinctly): %+v", got)
	}
}

func TestCanonicalLang(t *testing.T) {
	cases := map[string]string{"go": ".go", "ts": ".tsx", "typescript": ".tsx", "py": ".pyi", "cpp": ".h", "c++": ".h"}
	for name, wantExt := range cases {
		exts, ok := CanonicalLang(name)
		if !ok {
			t.Errorf("CanonicalLang(%q) not recognized", name)
			continue
		}
		found := false
		for _, e := range exts {
			if e == wantExt {
				found = true
			}
		}
		if !found {
			t.Errorf("CanonicalLang(%q) = %v, missing %q", name, exts, wantExt)
		}
	}
	if _, ok := CanonicalLang("rust"); ok {
		t.Error("CanonicalLang(rust) should be unknown")
	}
}
