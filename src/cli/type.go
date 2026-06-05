package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newTypeCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "type <location>",
		Short: "show the type/signature at a position",
		Long:  locationNote,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			resp := runOp("type", args[0], nil)
			if asJSON {
				printJSON(map[string]any{"hover": resp.Hover})
				return nil
			}
			if resp.Hover == nil {
				fmt.Fprintln(os.Stderr, "no type information")
				os.Exit(2)
			}
			fmt.Println(stripCodeFences(*resp.Hover))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "structured output")
	return cmd
}
