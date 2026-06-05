package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/srossross/idit/src/ipc"
)

func newMvCmd() *cobra.Command {
	var asJSON, dryRun bool
	cmd := &cobra.Command{
		Use:   "mv <from> <to>",
		Short: "move/rename a file, fixing imports",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			from := resolveCwd(args[0])
			to := resolveCwd(args[1])
			// `mv file dir/` → move into the directory, keeping the filename.
			if info, err := os.Stat(to); err == nil && info.IsDir() {
				to = filepath.Join(to, filepath.Base(from))
			}

			sock, server := socketForFile(from)
			resp := sendOp(sock, server.Name, ipc.Request{Op: "mv", From: from, To: to, DryRun: dryRun})
			if !resp.OK {
				fail("%s", resp.Error)
			}
			if asJSON {
				printJSON(resp)
				return nil
			}

			verb := "would move"
			if resp.Applied {
				verb = "moved"
			}
			fmt.Fprintf(os.Stderr, "%s %s -> %s: %d import edit(s) across %d file(s)\n", verb, from, to, len(resp.Sites), resp.FileCount)
			sorted := append([]ipc.EditSite(nil), resp.Sites...)
			sort.SliceStable(sorted, func(i, j int) bool { return editSiteLess(sorted[i], sorted[j]) })
			for _, s := range sorted {
				fmt.Printf("%s:%d:%d\n", s.File, s.Line, s.Col)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "preview the edits without writing")
	cmd.Flags().BoolVar(&asJSON, "json", false, "structured output")
	return cmd
}
