package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// newHelpCmd replaces cobra's default help command: bare `idit help` prints a
// full reference — every command with its flags, including nested subcommands
// like `server add` — while `idit help <command>` shows that command's own help.
func newHelpCmd(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "help [command]",
		Short: "full reference for every command and flag",
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				c, _, err := root.Find(args)
				if err != nil || c == nil {
					return fmt.Errorf("unknown help topic %q", strings.Join(args, " "))
				}
				return c.Help()
			}
			printReference(root)
			return nil
		},
	}
}

func printReference(root *cobra.Command) {
	fmt.Printf("%s — %s\n", root.Name(), root.Short)
	if root.Long != "" {
		fmt.Printf("\n%s\n", root.Long)
	}
	fmt.Println("\nCOMMANDS")
	writeCommands(root)
}

// writeCommands prints each available subcommand (and its descendants) with its
// usage line, summary, and own flags.
func writeCommands(parent *cobra.Command) {
	for _, c := range parent.Commands() {
		if c.Hidden || !c.IsAvailableCommand() {
			continue
		}
		switch c.Name() {
		case "help", "completion": // cobra's own scaffolding
			continue
		}
		fmt.Printf("\n  %s\n", c.UseLine())
		fmt.Printf("    %s\n", c.Short)
		for _, line := range flagLines(c) {
			fmt.Printf("    %s\n", line) // pflag already leads with 2 spaces → 4 total
		}
		writeCommands(c)
	}
}

// flagLines returns the command's own flags, one per line, excluding the
// ubiquitous --help. pflag's own column alignment (and 2-space lead) is kept.
func flagLines(c *cobra.Command) []string {
	fs := pflag.NewFlagSet("", pflag.ContinueOnError)
	c.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if f.Name != "help" {
			fs.AddFlag(f)
		}
	})
	usage := strings.TrimRight(fs.FlagUsages(), "\n")
	if usage == "" {
		return nil
	}
	return strings.Split(usage, "\n")
}
