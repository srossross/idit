package cli

import (
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/srossross/clidit/src/ipc"
)

func newRenameCmd() *cobra.Command {
	var asJSON, dryRun bool
	cmd := &cobra.Command{
		Use:   "rename <file:line:col> <newName>",
		Short: "rename a symbol project-wide",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			target, err := parseLocator(args[0])
			if err != nil {
				fail("%v", err)
			}
			newName := args[1]
			file := resolveCwd(target.File)
			sock, server := socketForFile(file)

			resp := sendOp(sock, server.Name, ipc.Request{
				Op: "rename", File: file, Line: target.Line, Col: target.Col, NewName: newName, DryRun: dryRun,
			})
			if !resp.OK {
				fail("%s", resp.Error)
			}
			if asJSON {
				printJSON(resp)
				return nil
			}

			verb := "would rename"
			if resp.Applied {
				verb = "renamed"
			}
			fmt.Fprintf(os.Stderr, "%s → '%s': %d edit(s) across %d file(s)\n", verb, newName, len(resp.Sites), resp.FileCount)
			sorted := append([]ipc.EditSite(nil), resp.Sites...)
			sort.SliceStable(sorted, func(i, j int) bool { return editSiteLess(sorted[i], sorted[j]) })
			for _, s := range sorted {
				fmt.Printf("%s:%d:%d\n", s.File, s.Line, s.Col)
			}
			for _, op := range resp.ResourceOps {
				what := op.File
				if op.Kind == "rename" {
					what = fmt.Sprintf("%s -> %s", op.From, op.To)
				}
				fmt.Fprintf(os.Stderr, "  %s: %s\n", op.Kind, what)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "preview the edits without writing")
	cmd.Flags().BoolVar(&asJSON, "json", false, "structured output")
	return cmd
}
