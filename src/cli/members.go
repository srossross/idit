package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/srossross/clidit/src/ipc"
)

func newMembersCmd() *cobra.Command {
	var asJSON, noDetail bool
	var filter nameFlags
	cmd := &cobra.Command{
		Use:   "members <location>",
		Short: "list members available after a `.`",
		Long:  locationNote,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			detail := !noDetail
			filter.matcher() // validate --grep up front; daemon filters server-side
			resp := runOp("members", args[0], func(req *ipc.Request) {
				req.Detail = detail
				req.Prefix = filter.prefix
				req.Grep = filter.grep
				req.IgnoreCase = filter.ignoreCase
			})
			members := resp.Members
			if asJSON {
				printJSON(orEmptyMembers(members))
				return nil
			}
			if len(members) == 0 {
				fmt.Fprintln(os.Stderr, "no members")
				os.Exit(2)
			}
			width := 0
			for _, m := range members {
				if len(m.Kind) > width {
					width = len(m.Kind)
				}
			}
			if width > 12 {
				width = 12
			}
			for _, m := range members {
				header := fmt.Sprintf("%-*s  %s", width, m.Kind, m.Label)
				switch {
				case m.Detail == "":
					fmt.Println(header)
				case !strings.Contains(m.Detail, "\n"):
					fmt.Printf("%s  %s\n", header, m.Detail)
				default:
					fmt.Println(header)
					for line := range strings.SplitSeq(m.Detail, "\n") {
						fmt.Printf("| %s\n", line)
					}
				}
			}
			if resp.Incomplete {
				fmt.Fprintln(os.Stderr, "(list incomplete — server returned a partial set)")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&noDetail, "no-detail", false, "skip signatures/types (faster)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "structured output")
	filter.add(cmd)
	return cmd
}
