package lsputil

import (
	"encoding/json"
	"testing"
)

// Reference strings captured from Node's pathToFileURL(path).href on POSIX.
var nodeURIcases = []struct{ path, uri string }{
	{"/Users/sean/a.ts", "file:///Users/sean/a.ts"},
	{"/tmp/with space.ts", "file:///tmp/with%20space.ts"},
	{"/p/café.ts", "file:///p/caf%C3%A9.ts"},
	{"/a/b#c.ts", "file:///a/b%23c.ts"},
	{"/a/b?c.ts", "file:///a/b%3Fc.ts"},
	{"/a/b@c.ts", "file:///a/b@c.ts"},
	{"/a/b+c.ts", "file:///a/b+c.ts"},
	{"/a/x:y.ts", "file:///a/x:y.ts"},
	{"/a/100%.ts", "file:///a/100%25.ts"},
	{"/a/b&c=d.ts", "file:///a/b&c=d.ts"},
	{"/a/(x).ts", "file:///a/(x).ts"},
}

func TestFileToURIMatchesNode(t *testing.T) {
	for _, c := range nodeURIcases {
		if got := FileToURI(c.path); got != c.uri {
			t.Errorf("FileToURI(%q) = %q, want %q", c.path, got, c.uri)
		}
	}
}

func TestURIToFileMatchesNode(t *testing.T) {
	for _, c := range nodeURIcases {
		if got := URIToFile(c.uri); got != c.path {
			t.Errorf("URIToFile(%q) = %q, want %q", c.uri, got, c.path)
		}
	}
}

func TestURIRoundTrip(t *testing.T) {
	paths := []string{
		"/Users/sean/a.ts", "/tmp/with space.ts", "/p/café.ts",
		"/a/b#c.ts", "/a/100%.ts", "/weird/{}<>`\".ts",
	}
	for _, p := range paths {
		if got := URIToFile(FileToURI(p)); got != p {
			t.Errorf("round-trip %q -> %q", p, got)
		}
	}
}

func TestToLSPPosition(t *testing.T) {
	p := ToLSPPosition(3, 5)
	if p.Line != 2 || p.Character != 4 {
		t.Fatalf("got %+v", p)
	}
}

func TestToSitesLocationArray(t *testing.T) {
	raw := json.RawMessage(`[{"uri":"file:///a.ts","range":{"start":{"line":4,"character":2},"end":{"line":6,"character":1}}}]`)
	sites := ToSites(raw)
	if len(sites) != 1 {
		t.Fatalf("want 1 site, got %d", len(sites))
	}
	s := sites[0]
	if s.File != "/a.ts" || s.Line != 5 || s.Col != 3 {
		t.Fatalf("bad jump pos: %+v", s)
	}
	if s.Range.StartLine != 5 || s.Range.EndLine != 7 || s.Range.EndCol != 2 {
		t.Fatalf("bad range: %+v", s.Range)
	}
}

func TestToSitesSingleLocation(t *testing.T) {
	raw := json.RawMessage(`{"uri":"file:///b.ts","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":3}}}`)
	sites := ToSites(raw)
	if len(sites) != 1 || sites[0].File != "/b.ts" || sites[0].Line != 1 {
		t.Fatalf("bad: %+v", sites)
	}
}

func TestToSitesLocationLink(t *testing.T) {
	raw := json.RawMessage(`[{"targetUri":"file:///c.ts","targetRange":{"start":{"line":10,"character":0},"end":{"line":15,"character":1}},"targetSelectionRange":{"start":{"line":10,"character":9},"end":{"line":10,"character":12}}}]`)
	sites := ToSites(raw)
	if len(sites) != 1 {
		t.Fatalf("want 1, got %d", len(sites))
	}
	s := sites[0]
	// Jump position is the selection (name), preview range is the full decl.
	if s.Line != 11 || s.Col != 10 {
		t.Fatalf("bad jump: %+v", s)
	}
	if s.Range.StartLine != 11 || s.Range.EndLine != 16 {
		t.Fatalf("bad range: %+v", s.Range)
	}
}

func TestToSitesNull(t *testing.T) {
	if got := ToSites(json.RawMessage(`null`)); len(got) != 0 {
		t.Fatalf("null should give 0 sites, got %d", len(got))
	}
}
