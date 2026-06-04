package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newDefCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "def <file:line:col>",
		Short: "find where a symbol is defined",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			resp := runOp("def", args[0], nil)
			locations := resp.Locations
			if asJSON {
				printJSON(orEmptySites(locations))
				return nil
			}
			if len(locations) == 0 {
				fmt.Fprintln(os.Stderr, "no definition found")
				os.Exit(2)
			}
			for _, loc := range locations {
				fmt.Printf("%s:%d:%d\n", loc.File, loc.Line, loc.Col)
				fmt.Println(renderPreview(loc))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "structured output")
	return cmd
}
