package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newLocateCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "locate <location>",
		Short: "resolve a symbol path (file#scope.name) to file:line:col",
		Long: "locate resolves a symbol path within a file to a position, descending\n" +
			"scope by scope. The path is dot-separated: foo.go#main.x finds the\n" +
			"declaration x inside main. Resolution is syntactic (tree-sitter), so an\n" +
			"ambiguous path (e.g. a shadowed local) lists every candidate and exits 3.\n" +
			"A plain file.ext:line:col target is echoed back after validation.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			arg := args[0]
			res, symErr := resolveSymbol(arg)
			// A plain file:line:col target (no #) has no candidates to show; just
			// validate and echo it.
			if symErr != nil && !strings.Contains(arg, "#") {
				loc, err := resolveLocation(arg)
				if err != nil {
					fail("%v", err)
				}
				if asJSON {
					printJSON(locateJSON(arg, false, []locateCandidate{{File: loc.File, Line: loc.Line, Col: loc.Col}}))
				} else {
					fmt.Printf("%s:%d:%d\n", loc.File, loc.Line, loc.Col)
				}
				return nil
			}
			if symErr != nil {
				fail("%v", symErr)
			}

			if asJSON {
				printJSON(locateJSON(arg, len(res.cands) > 1, res.cands))
			}
			switch len(res.cands) {
			case 1:
				if !asJSON {
					c := res.cands[0]
					fmt.Printf("%s:%d:%d\n", c.File, c.Line, c.Col)
				}
				return nil
			case 0:
				if !asJSON {
					fmt.Fprintf(os.Stderr, "idit: %v\n", res.notFound())
				}
				os.Exit(2)
			default:
				if !asJSON {
					fmt.Fprintf(os.Stderr, "idit: %s, please choose:\n", res.ambiguous().Error())
					printCandidates(res.cands)
				}
				os.Exit(3)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "structured output")
	return cmd
}

// locateJSON builds the --json object, coercing nil candidates to [].
func locateJSON(query string, ambiguous bool, cands []locateCandidate) map[string]any {
	if cands == nil {
		cands = []locateCandidate{}
	}
	return map[string]any{"query": query, "ambiguous": ambiguous, "candidates": cands}
}
