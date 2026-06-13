package cli

import (
	"regexp"
	"testing"

	"github.com/srossross/idit/src/treesitter"
)

func TestPlainReplaceMatches(t *testing.T) {
	src := []byte("aXbXc")
	got := plainReplaceMatches(src, regexp.MustCompile("X"), "YY")
	if len(got) != 2 {
		t.Fatalf("want 2 matches, got %d", len(got))
	}
	out, applied := applyReplacements(src, got)
	if string(out) != "aYYbYYc" {
		t.Fatalf("apply = %q, want %q", out, "aYYbYYc")
	}
	if len(applied) != 2 {
		t.Fatalf("applied = %d, want 2", len(applied))
	}
}

func TestPlainReplaceSubmatch(t *testing.T) {
	src := []byte("call(foo) and call(bar)")
	out, _ := applyReplacements(src, plainReplaceMatches(src, regexp.MustCompile(`call\((\w+)\)`), "invoke[$1]"))
	if string(out) != "invoke[foo] and invoke[bar]" {
		t.Fatalf("apply = %q", out)
	}
}

// TestApplyReplacementsOverlap confirms an overlapping match is dropped so
// splicing stays safe, and the dropped match is excluded from the applied set.
func TestApplyReplacementsOverlap(t *testing.T) {
	src := []byte("abcde")
	matches := []treesitter.ReplaceMatch{
		{StartByte: 0, EndByte: 3, New: "Z"}, // "abc" -> "Z"
		{StartByte: 2, EndByte: 4, New: "Q"}, // "cd" overlaps the first
	}
	out, applied := applyReplacements(src, matches)
	// Highest start wins: {2,4} applies first, then {0,3} overlaps it and is dropped.
	if string(out) != "abQe" {
		t.Fatalf("apply = %q, want %q", out, "abQe")
	}
	if len(applied) != 1 || applied[0].StartByte != 2 {
		t.Fatalf("applied = %+v, want only the {2,4} match", applied)
	}
}

func TestByteToLineCol(t *testing.T) {
	src := []byte("ab\ncd")
	if l, c := byteToLineCol(src, 4); l != 2 || c != 2 { // the 'd'
		t.Fatalf("byteToLineCol(4) = %d,%d, want 2,2", l, c)
	}
	// é is two bytes but one UTF-16 unit; column counts UTF-16 units.
	utf := []byte("x\ncafé")
	if l, c := byteToLineCol(utf, len(utf)); l != 2 || c != 5 {
		t.Fatalf("byteToLineCol(end) = %d,%d, want 2,5", l, c)
	}
}

func TestParseLangs(t *testing.T) {
	exts, err := parseLangs("go,ts")
	if err != nil {
		t.Fatalf("parseLangs: %v", err)
	}
	for _, want := range []string{".go", ".ts", ".tsx"} {
		if !exts[want] {
			t.Errorf("parseLangs(go,ts) missing %q", want)
		}
	}
	if _, err := parseLangs("rust"); err == nil {
		t.Error("parseLangs(rust) should error")
	}
	if got, err := parseLangs(""); got != nil || err != nil {
		t.Errorf("parseLangs(empty) = %v,%v, want nil,nil", got, err)
	}
}
