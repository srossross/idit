package cli

import (
	"github.com/spf13/cobra"

	"github.com/srossross/idit/src/lsputil"
)

// nameFlags holds the shared --prefix / --grep / --ignore-case values for the
// name-listing commands (members, symbol, outline).
type nameFlags struct {
	prefix     string
	grep       string
	ignoreCase bool
}

// add registers the shared name-filter flags on cmd.
func (f *nameFlags) add(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.prefix, "prefix", "", "only names with this prefix (case-sensitive unless -i)")
	cmd.Flags().StringVar(&f.grep, "grep", "", "only names matching this regex (RE2; case-insensitive with -i or (?i))")
	cmd.Flags().BoolVarP(&f.ignoreCase, "ignore-case", "i", false, "make --prefix and --grep case-insensitive")
}

// matcher compiles the flags into a NameMatcher, failing fast on a bad regex.
func (f *nameFlags) matcher() lsputil.NameMatcher {
	m, err := lsputil.NewNameMatcher(f.prefix, f.grep, f.ignoreCase)
	if err != nil {
		fail("invalid --grep regex: %v", err)
	}
	return m
}
