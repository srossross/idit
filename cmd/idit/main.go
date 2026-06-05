// Command idit exposes a language server from the command line. A single binary
// serves both roles: the short-lived CLI front-end, and — when re-executed with
// the hidden `__serve` subcommand — the long-lived per-workspace daemon.
package main

import (
	"os"

	"github.com/srossross/idit/src/cli"
	"github.com/srossross/idit/src/daemon"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "__serve" {
		daemon.Run(args[1:])
		return
	}
	cli.Run(args)
}
