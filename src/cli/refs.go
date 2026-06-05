package cli

import (
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/srossross/idit/src/lsputil"
)

func newRefsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "refs <location>",
		Short: "find all references to a symbol",
		Long:  locationNote,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			resp := runOp("refs", args[0], nil)
			locations := resp.Locations
			if asJSON {
				printJSON(orEmptySites(locations))
				return nil
			}
			if len(locations) == 0 {
				fmt.Fprintln(os.Stderr, "no references found")
				os.Exit(2)
			}
			sorted := append([]lsputil.Site(nil), locations...)
			sort.SliceStable(sorted, func(i, j int) bool { return siteLess(sorted[i], sorted[j]) })
			cache := lineCache{}
			for _, loc := range sorted {
				fmt.Printf("%s:%d:%d:%s\n", loc.File, loc.Line, loc.Col, cache.lineAt(loc.File, loc.Line))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "structured output")
	return cmd
}
