package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/srossross/clidit/src/ipc"
)

func newCheckCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "check <file>",
		Short: "report type errors/warnings in a file",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			file := resolveCwd(stripPosition(args[0]))
			sock, server := socketForFile(file)
			resp := sendOp(sock, server.Name, ipc.Request{Op: "check", File: file})
			if !resp.OK {
				fail("%s", resp.Error)
			}

			diags := resp.Diagnostics
			if asJSON {
				printJSON(orEmptyDiags(diags))
				return nil
			}
			if len(diags) == 0 {
				fmt.Fprintln(os.Stderr, "no problems")
				return nil // clean → exit 0
			}
			sort.SliceStable(diags, func(i, j int) bool {
				if diags[i].Line != diags[j].Line {
					return diags[i].Line < diags[j].Line
				}
				return diags[i].Col < diags[j].Col
			})
			hasError := false
			for _, d := range diags {
				tag := strings.TrimSpace(strings.Join(nonEmpty(d.Source, d.CodeString()), " "))
				suffix := ""
				if tag != "" {
					suffix = " [" + tag + "]"
				}
				message := collapseWhitespace(d.Message)
				fmt.Printf("%s:%d:%d: %s: %s%s\n", file, d.Line, d.Col, d.Severity, message, suffix)
				if d.Severity == "error" {
					hasError = true
				}
			}
			if hasError {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "structured output")
	return cmd
}
