package cli

import (
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"
)

func newCallersCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "callers <file:line:col>",
		Short: "find callers of a function",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			resp := runOp("callers", args[0], nil)
			callers := resp.Callers
			if asJSON {
				printJSON(orEmptyCallers(callers))
				return nil
			}
			if len(callers) == 0 {
				fmt.Fprintln(os.Stderr, "no callers found")
				os.Exit(2)
			}
			sort.SliceStable(callers, func(i, j int) bool { return callerLess(callers[i], callers[j]) })
			for _, c := range callers {
				fmt.Printf("%s:%d:%d  %s %s\n", c.File, c.Line, c.Col, c.Kind, c.Name)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "structured output")
	return cmd
}
