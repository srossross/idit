package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/srossross/clidit/src/ipc"
	"github.com/srossross/clidit/src/lsputil"
	"github.com/srossross/clidit/src/workspace"
)

func newStringCmd() *cobra.Command  { return newTokenSearchCmd("string", "string literals") }
func newCommentCmd() *cobra.Command { return newTokenSearchCmd("comment", "comments") }

// newTokenSearchCmd builds the shared `string`/`comment` command: it searches the
// project for a regex inside spans the language server classifies as the given
// token type. It mirrors `symbol` — query every configured server's daemon and
// merge the results.
func newTokenSearchCmd(op, label string) *cobra.Command {
	var asJSON bool
	var under string
	var ignoreCase bool
	cmd := &cobra.Command{
		Use:   op + " <regex>",
		Short: "search for a regex inside " + label + " across the project",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			query := args[0]
			if _, err := regexp.Compile(query); err != nil { // fail fast before any daemon round-trip
				fail("invalid regex: %v", err)
			}

			root := requireRoot()
			cfg, err := workspace.Load(root)
			if err != nil {
				fail("%v", err)
			}
			if len(cfg.Servers) == 0 {
				fail("no servers configured — run `idit server add <name>`")
			}

			var locations []lsputil.Site
			for _, server := range cfg.Servers {
				sock, err := ensureSocket(root, server)
				if err != nil {
					fail("%v", err)
				}
				resp := sendOp(sock, server.Name, ipc.Request{Op: op, Query: query, IgnoreCase: ignoreCase})
				if !resp.OK {
					fail("%s", resp.Error)
				}
				// A server that can't classify tokens reports it here instead of
				// failing the whole search; surface it but keep going.
				if resp.Message != "" {
					fmt.Fprintln(os.Stderr, "idit: "+resp.Message)
				}
				locations = append(locations, resp.Locations...)
			}

			if under != "" {
				locations = sitesUnder(locations, resolveCwd(under))
			}

			if asJSON {
				printJSON(orEmptySites(locations))
				return nil
			}
			if len(locations) == 0 {
				fmt.Fprintf(os.Stderr, "no %s matches found\n", op)
				os.Exit(2)
			}
			sort.SliceStable(locations, func(i, j int) bool { return siteLess(locations[i], locations[j]) })
			cache := lineCache{}
			for _, loc := range locations {
				fmt.Printf("%s:%d:%d:%s\n", loc.File, loc.Line, loc.Col, cache.lineAt(loc.File, loc.Line))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&under, "under", "", "only matches whose file is under this path (e.g. . for this workspace)")
	cmd.Flags().BoolVarP(&ignoreCase, "ignore-case", "i", false, "case-insensitive match")
	cmd.Flags().BoolVar(&asJSON, "json", false, "structured output")
	return cmd
}

// sitesUnder keeps the sites whose file lives within prefix (an absolute, cleaned
// directory path) — the []Site analog of symbolsUnder.
func sitesUnder(sites []lsputil.Site, prefix string) []lsputil.Site {
	kept := sites[:0]
	for _, s := range sites {
		file := filepath.Clean(s.File)
		if file == prefix || strings.HasPrefix(file, prefix+string(filepath.Separator)) {
			kept = append(kept, s)
		}
	}
	return kept
}
