package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/srossross/clidit/src/workspace"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init [path]",
		Short: "create an idit workspace (.idit/config.yml)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			target := "."
			if len(args) > 0 {
				target = args[0]
			}
			root := resolveCwd(target)
			if info, err := os.Stat(root); err != nil || !info.IsDir() {
				fail("not a directory: %s", root)
			}
			if err := os.MkdirAll(filepath.Join(root, workspace.StateDir), 0o750); err != nil {
				fail("%v", err)
			}
			cfgPath := workspace.ConfigPath(root)
			if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
				out, _ := workspace.Emit(workspace.IditConfig{})
				if err := os.WriteFile(cfgPath, out, 0o600); err != nil {
					fail("%v", err)
				}
			}
			fmt.Printf("initialized idit workspace at %s\n", root)
			fmt.Printf("add a server, e.g.:  idit server add tsserver\n")
			return nil
		},
	}
}
