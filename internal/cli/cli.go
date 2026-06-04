// Package cli implements the short-lived `idit` front-end: it parses argv, finds
// the workspace, ensures the right daemon is running, sends one request, and
// renders the reply.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/srossross/clidit/internal/ipc"
	"github.com/srossross/clidit/internal/workspace"
)

const requestTimeout = 15 * time.Second

// Run dispatches a CLI invocation (os.Args[1:]).
func Run(args []string) {
	var sub string
	var rest []string
	if len(args) > 0 {
		sub = args[0]
		rest = args[1:]
	}
	switch sub {
	case "init":
		cmdInit(rest)
	case "shutdown":
		cmdShutdown(rest)
	case "def":
		cmdDef(rest)
	case "refs":
		cmdRefs(rest)
	case "type":
		cmdType(rest)
	case "members":
		cmdMembers(rest)
	case "outline":
		cmdOutline(rest)
	case "symbol":
		cmdSymbol(rest)
	case "callers":
		cmdCallers(rest)
	case "check":
		cmdCheck(rest)
	case "rename":
		cmdRename(rest)
	case "mv":
		cmdMv(rest)
	case "extract":
		cmdExtract(rest)
	case "server":
		cmdServer(rest)
	case "", "-h", "--help":
		usage(0)
	default:
		fail("unknown command: %s", sub)
	}
}

// --- shared helpers ---

func requireRoot() string {
	cwd, _ := os.Getwd()
	root, ok := workspace.FindRoot(cwd)
	if !ok {
		fail("not an idit workspace — run `idit init` first")
	}
	return root
}

// socketForFile resolves the daemon that handles file: find the workspace, load
// its config, pick the server for the file's extension, and ensure it's running.
func socketForFile(file string) (string, workspace.ServerConfig) {
	root, ok := workspace.FindRoot(filepath.Dir(file))
	if !ok {
		fail("not an idit workspace — run `idit init` first")
	}
	cfg, err := workspace.Load(root)
	if err != nil {
		fail("%v", err)
	}
	server, ok := workspace.ServerForFile(cfg, file)
	if !ok {
		ext := ""
		if dot := filepath.Ext(file); dot != "" {
			ext = dot
		}
		known := "(none)"
		if names := serverNames(cfg); names != "" {
			known = names
		}
		fail("no server configured for %s files (configured: %s; add one with `idit server add`)", ext, known)
	}
	sock, err := ensureSocket(root, server)
	if err != nil {
		fail("%v", err)
	}
	return sock, server
}

func sendOp(sock, serverName string, req ipc.Request) ipc.Response {
	resp, err := ipc.RequestDaemon(sock, req, requestTimeout)
	if err != nil {
		if errors.Is(err, ipc.ErrDaemonUnreachable) {
			fail("daemon socket is stale or unreachable (%s).\n  restart it with:  idit __serve %s", sock, serverName)
		}
		fail("%v", err)
	}
	return resp
}

// runOp parses flags + a positional file:line:col target, resolves the owning
// daemon, and runs a positional op. apply may set extra request fields.
func runOp(verb string, args []string, apply func(*ipc.Request)) (ipc.Response, bool) {
	asJSON := hasFlag(args, "--json")
	targetArg, ok := firstPositional(args)
	if !ok {
		fail("usage: idit %s <file:line:col> [--json]", verb)
	}
	target, err := parseLocator(targetArg)
	if err != nil {
		fail("%v", err)
	}
	file := resolveCwd(target.File)
	sock, server := socketForFile(file)

	req := ipc.Request{Op: verb, File: file, Line: target.Line, Col: target.Col}
	if apply != nil {
		apply(&req)
	}
	resp := sendOp(sock, server.Name, req)
	if !resp.OK {
		fail("%s", resp.Error)
	}
	return resp, asJSON
}

func resolveCwd(p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, p)
}

func serverNames(cfg workspace.IditConfig) string {
	out := ""
	for i, s := range cfg.Servers {
		if i > 0 {
			out += ", "
		}
		out += s.Name
	}
	return out
}

func printJSON(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		fail("%v", err)
	}
	os.Stdout.Write(append(b, '\n'))
}

func usage(code int) {
	fmt.Print(`idit — expose a language server from the command line

usage:
  idit init [path]             create an idit workspace (.idit/config.yml)
  idit server add <name>       add a language server (preset or --command/--ext)
  idit server list|remove      manage configured servers
  idit shutdown [--force]      stop this workspace's daemon(s)
  idit def <file:line:col>     find where a symbol is defined
  idit refs <file:line:col>    find all references to a symbol
  idit type <file:line:col>    show the type/signature at a position
  idit members <file:line:col> list members available after a ` + "`.`" + `
    --no-detail                skip signatures/types (faster)
  idit outline <file>          list the symbols in a file
    --kind <kind>              only this kind (e.g. class, function)
  idit symbol <query>          search symbols across the project
  idit callers <file:line:col> find callers of a function
  idit check <file>            report type errors/warnings in a file
  idit rename <file:line:col> <newName>   rename a symbol project-wide
  idit mv <from> <to>          move/rename a file, fixing imports
  idit extract <file:l:c-l:c>  extract a selection (then ` + "`idit rename`" + `)
    --scope <n>                pick a refactoring when several apply
    -n, --dry-run              preview the edits without writing
    --json                     structured output

daemons start automatically; configure servers in .idit/config.yml
`)
	os.Exit(code)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "idit: "+format+"\n", args...)
	os.Exit(1)
}
