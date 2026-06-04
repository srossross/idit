package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/srossross/clidit/src/ipc"
	"github.com/srossross/clidit/src/lsputil"
	"github.com/srossross/clidit/src/workspace"
)

func newSymbolCmd() *cobra.Command {
	var asJSON bool
	var under string
	var fuzzy, strict bool
	var kind string
	var filter nameFlags
	cmd := &cobra.Command{
		Use:   "symbol <query>",
		Short: "search symbols across the project",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			query := args[0]
			match := filter.matcher() // validate --grep before any daemon round-trip
			var wantKind map[string]bool
			if kind != "" {
				var err error
				if wantKind, err = resolveKind(kind); err != nil {
					fail("%v", err)
				}
			}

			// Project-wide and language-agnostic: query every configured
			// server's daemon and merge the results.
			root := requireRoot()
			cfg, err := workspace.Load(root)
			if err != nil {
				fail("%v", err)
			}
			if len(cfg.Servers) == 0 {
				fail("no servers configured — run `idit server add <name>`")
			}

			var symbols []lsputil.FoundSymbol
			for _, server := range cfg.Servers {
				sock, err := ensureSocket(root, server)
				if err != nil {
					fail("%v", err)
				}
				resp := sendOp(sock, server.Name, ipc.Request{Op: "symbol", Query: query})
				if !resp.OK {
					fail("%s", resp.Error)
				}
				symbols = append(symbols, resp.Symbols...)
			}

			// workspace/symbol spans the server's whole build graph (deps,
			// locally-replaced modules), which can reach outside this workspace.
			// --under keeps only symbols whose file is within the given path.
			if under != "" {
				symbols = symbolsUnder(symbols, resolveCwd(under))
			}
			// --strict (exact name vs query), --prefix/--grep, and --kind narrow
			// the returned set further.
			symbols = filterFound(symbols, query, strict, match, wantKind)

			if asJSON {
				printJSON(orEmptyFound(symbols))
				return nil
			}
			if len(symbols) == 0 {
				fmt.Fprintln(os.Stderr, "no symbols found")
				os.Exit(2)
			}
			sort.SliceStable(symbols, func(i, j int) bool {
				if symbols[i].Name != symbols[j].Name {
					return symbols[i].Name < symbols[j].Name
				}
				if symbols[i].File != symbols[j].File {
					return symbols[i].File < symbols[j].File
				}
				return symbols[i].Line < symbols[j].Line
			})
			for _, s := range symbols {
				container := ""
				if s.Container != "" {
					container = fmt.Sprintf("  (%s)", s.Container)
				}
				fmt.Printf("%s:%d:%d  %s %s%s\n", s.File, s.Line, s.Col, s.Kind, s.Name, container)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&under, "under", "", "only symbols whose file is under this path (e.g. . for this workspace)")
	cmd.Flags().BoolVar(&fuzzy, "fuzzy", false, "fuzzy match the query (server default)")
	cmd.Flags().BoolVar(&strict, "strict", false, "keep only names equal to the query (case-insensitive)")
	cmd.MarkFlagsMutuallyExclusive("fuzzy", "strict")
	cmd.Flags().StringVar(&kind, "kind", "", "only this kind (e.g. func, class, type)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "structured output")
	filter.add(cmd)
	return cmd
}

// filterFound narrows a workspace/symbol result by the post-query CLI filters:
// --strict re-checks names for case-insensitive equality with the query
// (dropping loose fuzzy hits); --prefix/--grep narrow by name via match; --kind
// keeps only the requested kind(s) when wantKind is non-nil.
func filterFound(symbols []lsputil.FoundSymbol, query string, strict bool, match lsputil.NameMatcher, wantKind map[string]bool) []lsputil.FoundSymbol {
	kept := symbols[:0]
	for _, s := range symbols {
		if strict && !strings.EqualFold(s.Name, query) {
			continue
		}
		if match.Active() && !match.Match(s.Name) {
			continue
		}
		if wantKind != nil && !wantKind[s.Kind] {
			continue
		}
		kept = append(kept, s)
	}
	return kept
}

// symbolsUnder keeps the symbols whose file lives within prefix (an absolute,
// cleaned directory path), so callers can restrict a project-wide search to the
// current workspace or a subtree.
func symbolsUnder(symbols []lsputil.FoundSymbol, prefix string) []lsputil.FoundSymbol {
	kept := symbols[:0]
	for _, s := range symbols {
		file := filepath.Clean(s.File)
		if file == prefix || strings.HasPrefix(file, prefix+string(filepath.Separator)) {
			kept = append(kept, s)
		}
	}
	return kept
}
