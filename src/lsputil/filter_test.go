package lsputil

import "testing"

func TestNameMatcher(t *testing.T) {
	cases := []struct {
		name       string
		prefix     string
		grep       string
		ignoreCase bool
		input      string
		want       bool
	}{
		{"no filters matches all", "", "", false, "anything", true},
		{"prefix case-sensitive hit", "Set", "", false, "SetName", true},
		{"prefix case-sensitive miss", "Set", "", false, "setName", false},
		{"prefix ignore-case hit", "set", "", true, "SetName", true},
		{"grep regex hit", "", "^air.*ne$", false, "airplane", true},
		{"grep regex miss", "", "^air.*ne$", false, "airfield", false},
		{"grep ignore-case", "", "handler", true, "MyHandlerImpl", true},
		{"prefix and grep both required", "Get", "User$", false, "GetUser", true},
		{"prefix ok grep fail", "Get", "User$", false, "GetUserName", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := NewNameMatcher(c.prefix, c.grep, c.ignoreCase)
			if err != nil {
				t.Fatalf("NewNameMatcher: %v", err)
			}
			if got := m.Match(c.input); got != c.want {
				t.Errorf("Match(%q) = %v, want %v", c.input, got, c.want)
			}
		})
	}
}

func TestNameMatcherInvalidRegex(t *testing.T) {
	if _, err := NewNameMatcher("", "[", false); err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestNameMatcherActive(t *testing.T) {
	inactive, _ := NewNameMatcher("", "", true) // -i alone is not a filter
	if inactive.Active() {
		t.Error("matcher with no prefix/grep should be inactive")
	}
	active, _ := NewNameMatcher("x", "", false)
	if !active.Active() {
		t.Error("matcher with a prefix should be active")
	}
}
