package cli

import (
	"fmt"
	"strings"

	"github.com/srossross/idit/src/lsputil"
)

// kindAliases maps user-friendly shorthands to canonical LSP kind names (see
// lsputil symbolKind). A single token may expand to several kinds.
var kindAliases = map[string][]string{
	"func":  {"function"},
	"fn":    {"function"},
	"type":  {"struct", "interface", "class", "type-param"},
	"var":   {"variable"},
	"const": {"constant"},
	"iface": {"interface"},
	"ctor":  {"constructor"},
}

// resolveKind expands a --kind token into the set of canonical kind names to
// match against FoundSymbol.Kind/OutlineNode.Kind. An unaliased token is treated
// as a canonical name as-is. It errors (listing valid kinds) when nothing
// resolves to a known kind, so an unknown --kind fails loudly instead of
// silently returning no results.
func resolveKind(v string) (map[string]bool, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	names := kindAliases[v]
	if names == nil {
		names = []string{v}
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		if lsputil.KnownKind(n) {
			want[n] = true
		}
	}
	if len(want) == 0 {
		return nil, fmt.Errorf("unknown kind %q; valid kinds: %s",
			v, strings.Join(lsputil.KindNames(), ", "))
	}
	return want, nil
}
