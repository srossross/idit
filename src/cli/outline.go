package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/srossross/clidit/src/ipc"
	"github.com/srossross/clidit/src/lsputil"
)

func newOutlineCmd() *cobra.Command {
	var asJSON bool
	var kind string
	var filter nameFlags
	cmd := &cobra.Command{
		Use:   "outline <file>",
		Short: "list the symbols in a file",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			hasKind := c.Flags().Changed("kind")
			match := filter.matcher()
			var wantKind map[string]bool
			if hasKind {
				var err error
				if wantKind, err = resolveKind(kind); err != nil {
					fail("%v", err)
				}
			}
			file := resolveCwd(args[0])
			sock, server := socketForFile(file)
			resp := sendOp(sock, server.Name, ipc.Request{Op: "outline", File: file})
			if !resp.OK {
				fail("%s", resp.Error)
			}

			tree := resp.Outline
			if asJSON {
				printJSON(orEmptyOutline(tree))
				return nil
			}
			// Any of --kind/--prefix/--grep switches to a flat list of every
			// matching node (at any depth); otherwise show the full tree.
			if hasKind || match.Active() {
				var matches []lsputil.OutlineNode
				for _, n := range flattenOutline(tree) {
					if wantKind != nil && !wantKind[n.Kind] {
						continue
					}
					if !match.Match(n.Name) {
						continue
					}
					matches = append(matches, n)
				}
				if len(matches) == 0 {
					fmt.Fprintln(os.Stderr, "no matching symbols")
					os.Exit(2)
				}
				for _, n := range matches {
					fmt.Printf("%s:%d:%d  %s %s\n", file, n.Line, n.Col, n.Kind, n.Name)
				}
				return nil
			}
			if len(tree) == 0 {
				fmt.Fprintln(os.Stderr, "no symbols")
				os.Exit(2)
			}
			var sb strings.Builder
			printOutline(&sb, file, tree, 0)
			fmt.Print(sb.String())
			return nil
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "", "only this kind (e.g. func, class, type)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "structured output")
	filter.add(cmd)
	return cmd
}
