package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/srossross/clidit/src/ipc"
)

func newExtractCmd() *cobra.Command {
	var asJSON, dryRun bool
	var scope string
	cmd := &cobra.Command{
		Use:   "extract <range>",
		Short: "extract a selection (then `idit rename`)",
		Long:  rangeNote,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			r := mustResolveRange(args[0])
			file := resolveCwd(r.File)
			sock, server := socketForFile(file)
			resp := sendOp(sock, server.Name, ipc.Request{
				Op: "extract", File: file,
				StartLine: r.StartLine, StartCol: r.StartCol, EndLine: r.EndLine, EndCol: r.EndCol,
				Scope: scope, DryRun: dryRun,
			})
			if asJSON {
				printJSON(resp)
				return nil
			}
			if !resp.OK {
				fail("%s", resp.Error)
			}

			if resp.Mode == "list" {
				fmt.Fprintln(os.Stderr, "multiple refactorings — pick one with --scope <n>:")
				for _, c := range resp.Candidates {
					fmt.Printf("%d  %s\n", c.Index, c.Title)
				}
				return nil
			}

			verb := "extracted"
			if resp.Mode == "preview" {
				verb = "would extract"
			}
			fmt.Fprintf(os.Stderr, "%s: %s\n", verb, resp.Chosen)
			for _, s := range resp.Sites {
				fmt.Printf("%s:%d:%d\n", s.File, s.Line, s.Col)
			}
			if p := resp.Placeholder; p != nil {
				fmt.Printf("%s:%d:%d  %s\n", p.File, p.Line, p.Col, p.Name)
				fmt.Fprintf(os.Stderr, "  name it:  idit rename %s:%d:%d <newName>\n", p.File, p.Line, p.Col)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "pick a refactoring when several apply")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "preview the edits without writing")
	cmd.Flags().BoolVar(&asJSON, "json", false, "structured output")
	return cmd
}
