package cli

import "testing"

func TestParseLocator(t *testing.T) {
	cases := []struct {
		in        string
		file      string
		line, col int
		wantErr   bool
	}{
		{"a.ts:10:5", "a.ts", 10, 5, false},
		{"a.ts:10", "a.ts", 10, 1, false}, // col defaults to 1
		{"/abs/path/file.ts:3:2", "/abs/path/file.ts", 3, 2, false},
		{"weird:name.ts:7:1", "weird:name.ts", 7, 1, false}, // path with colon
		{"justafile", "", 0, 0, true},
		{"a.ts:notanumber", "", 0, 0, true},
	}
	for _, c := range cases {
		loc, err := parseLocator(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseLocator(%q) expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseLocator(%q): %v", c.in, err)
			continue
		}
		if loc.File != c.file || loc.Line != c.line || loc.Col != c.col {
			t.Errorf("parseLocator(%q) = %+v, want %s:%d:%d", c.in, loc, c.file, c.line, c.col)
		}
	}
}

func TestParseRange(t *testing.T) {
	r, err := parseRange("a.ts:1:5-3:10")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if r.File != "a.ts" || r.StartLine != 1 || r.StartCol != 5 || r.EndLine != 3 || r.EndCol != 10 {
		t.Fatalf("bad range: %+v", r)
	}
	// Path containing a dash; last '-' separates the end.
	r, err = parseRange("with-dash.ts:2:1-2:4")
	if err != nil || r.File != "with-dash.ts" || r.EndCol != 4 {
		t.Fatalf("dash path range wrong: %+v err=%v", r, err)
	}
	if _, err := parseRange("a.ts:1:5"); err == nil {
		t.Fatal("missing end should error")
	}
}

func TestStripPosition(t *testing.T) {
	cases := map[string]string{
		"a.ts:10:5":    "a.ts",
		"a.ts:10":      "a.ts",
		"a.ts":         "a.ts",
		"weird:n.ts:3": "weird:n.ts",
	}
	for in, want := range cases {
		if got := stripPosition(in); got != want {
			t.Errorf("stripPosition(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFlagValue(t *testing.T) {
	args := []string{"--command", "foo bar", "--ext=.a,.b", "pos"}
	if v, ok := flagValue(args, "--command"); !ok || v != "foo bar" {
		t.Errorf("space form: %q ok=%v", v, ok)
	}
	if v, ok := flagValue(args, "--ext"); !ok || v != ".a,.b" {
		t.Errorf("equals form: %q ok=%v", v, ok)
	}
	if _, ok := flagValue(args, "--missing"); ok {
		t.Error("missing flag should be absent")
	}
}

func TestPositionals(t *testing.T) {
	args := []string{"--scope", "2", "file.ts:1:1", "--json", "extra"}
	got := positionals(args, []string{"--scope"})
	want := []string{"file.ts:1:1", "extra"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("positionals = %v, want %v", got, want)
	}
}
