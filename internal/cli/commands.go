package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/srossross/clidit/internal/ipc"
	"github.com/srossross/clidit/internal/lsputil"
	"github.com/srossross/clidit/internal/workspace"
)

func killSignal() syscall.Signal { return syscall.SIGKILL }

// --- init / server config ---

func cmdInit(args []string) {
	target := "."
	if p := positionals(args, nil); len(p) > 0 {
		target = p[0]
	}
	root := resolveCwd(target)
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		fail("not a directory: %s", root)
	}
	os.MkdirAll(filepath.Join(root, workspace.StateDir), 0o755)
	cfgPath := workspace.ConfigPath(root)
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		out, _ := workspace.Emit(workspace.IditConfig{})
		os.WriteFile(cfgPath, out, 0o644)
	}
	fmt.Printf("initialized idit workspace at %s\n", root)
	fmt.Printf("add a server, e.g.:  idit server add tsserver\n")
}

func cmdServer(args []string) {
	var sub string
	var rest []string
	if len(args) > 0 {
		sub = args[0]
		rest = args[1:]
	}
	switch sub {
	case "add":
		cmdServerAdd(rest)
	case "list":
		cmdServerList()
	case "remove", "rm":
		cmdServerRemove(rest)
	default:
		fail("usage: idit server <add|list|remove> …\n  presets: %s", strings.Join(workspace.PresetNames(), ", "))
	}
}

func cmdServerAdd(args []string) {
	pos := positionals(args, []string{"--command", "--ext", "--lang"})
	if len(pos) == 0 {
		fail("usage: idit server add <name> [--command \"<cmd>\"] [--ext .a,.b] [--lang <id>]\n  presets: %s",
			strings.Join(workspace.PresetNames(), ", "))
	}
	name := pos[0]

	root := requireRoot()
	cfg, err := workspace.Load(root)
	if err != nil {
		fail("%v", err)
	}
	if _, exists := workspace.ServerByName(cfg, name); exists {
		fail("server %q is already configured", name)
	}

	command, hasCommand := flagValue(args, "--command")
	ext, hasExt := flagValue(args, "--ext")
	lang, hasLang := flagValue(args, "--lang")
	preset, hasPreset := workspace.Presets[name]

	var server workspace.ServerConfig
	switch {
	case hasPreset && !hasCommand:
		server = preset
		server.Name = name
	case hasCommand && hasExt:
		exts := strings.Split(ext, ",")
		for i := range exts {
			exts[i] = strings.ToLower(strings.TrimSpace(exts[i]))
		}
		langID := name
		if hasLang {
			langID = lang
		}
		server = workspace.ServerConfig{
			Name:       name,
			Kind:       "lsp",
			Command:    strings.Fields(command),
			Extensions: exts,
			LanguageID: langID,
		}
	default:
		fail("unknown preset %q — provide --command and --ext to define a custom server\n  presets: %s",
			name, strings.Join(workspace.PresetNames(), ", "))
	}

	cfg.Servers = append(cfg.Servers, server)
	out, err := workspace.Emit(cfg)
	if err != nil {
		fail("%v", err)
	}
	os.WriteFile(workspace.ConfigPath(root), out, 0o644)
	fmt.Printf("added server %q [%s]\n", name, strings.Join(server.Extensions, " "))
}

func cmdServerList() {
	cfg, err := workspace.Load(requireRoot())
	if err != nil {
		fail("%v", err)
	}
	if len(cfg.Servers) == 0 {
		fmt.Fprintln(os.Stderr, "no servers configured — add one with `idit server add <name>`")
		return
	}
	for _, s := range cfg.Servers {
		fmt.Printf("%s  [%s]  %s\n", s.Name, strings.Join(s.Extensions, " "), strings.Join(s.Command, " "))
	}
}

func cmdServerRemove(args []string) {
	pos := positionals(args, nil)
	if len(pos) == 0 {
		fail("usage: idit server remove <name>")
	}
	name := pos[0]
	root := requireRoot()
	cfg, err := workspace.Load(root)
	if err != nil {
		fail("%v", err)
	}
	if _, ok := workspace.ServerByName(cfg, name); !ok {
		fail("no server %q configured", name)
	}
	kept := cfg.Servers[:0]
	for _, s := range cfg.Servers {
		if s.Name != name {
			kept = append(kept, s)
		}
	}
	cfg.Servers = kept
	out, err := workspace.Emit(cfg)
	if err != nil {
		fail("%v", err)
	}
	os.WriteFile(workspace.ConfigPath(root), out, 0o644)
	fmt.Printf("removed server %q\n", name)
}

func cmdShutdown(args []string) {
	force := hasFlag(args, "--force", "-f")
	cwd, _ := os.Getwd()
	root, ok := workspace.FindRoot(cwd)
	if !ok {
		fail("not an idit workspace — nothing to shut down")
	}

	keys := runningKeys(filepath.Join(root, workspace.StateDir))
	if len(keys) == 0 {
		fmt.Fprintln(os.Stderr, "no daemons running")
		return
	}

	for _, key := range keys {
		sock := workspace.SocketPath(root, key)
		if force {
			forceKill(root, key)
			continue
		}
		_, err := ipc.RequestDaemon(sock, ipc.Request{Op: "shutdown"}, requestTimeout)
		if err == nil {
			fmt.Fprintf(os.Stderr, "%s: shutting down\n", key)
		} else if err == ipc.ErrDaemonUnreachable {
			os.Remove(sock) // stale socket
			fmt.Fprintf(os.Stderr, "%s: not running (cleared stale socket)\n", key)
		} else {
			fmt.Fprintf(os.Stderr, "%s: %v\n", key, err)
		}
	}
}

// runningKeys lists language keys with a socket or pid file in the state dir.
func runningKeys(stateDir string) []string {
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	var keys []string
	add := func(k string) {
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".sock") {
			add(strings.TrimSuffix(name, ".sock"))
		} else if strings.HasSuffix(name, ".pid") {
			add(strings.TrimSuffix(name, ".pid"))
		}
	}
	return keys
}

// forceKill kills a daemon via its pid file (for a wedged, unresponsive daemon).
func forceKill(root, key string) {
	pidFile := workspace.PidPath(root, key)
	data, err := os.ReadFile(pidFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: no pid file\n", key)
		return
	}
	pid, _ := atoi(strings.TrimSpace(string(data)))
	if proc, err := os.FindProcess(pid); err == nil && proc.Signal(killSignal()) == nil {
		fmt.Fprintf(os.Stderr, "%s: killed (pid %d)\n", key, pid)
	} else {
		fmt.Fprintf(os.Stderr, "%s: already gone (pid %d)\n", key, pid)
	}
	os.Remove(pidFile)
	os.Remove(workspace.SocketPath(root, key))
}

// --- query commands ---

func cmdDef(args []string) {
	resp, asJSON := runOp("def", args, nil)
	locations := resp.Locations
	if asJSON {
		printJSON(orEmptySites(locations))
		return
	}
	if len(locations) == 0 {
		fmt.Fprintln(os.Stderr, "no definition found")
		os.Exit(2)
	}
	for _, loc := range locations {
		fmt.Printf("%s:%d:%d\n", loc.File, loc.Line, loc.Col)
		fmt.Println(renderPreview(loc))
	}
}

func cmdRefs(args []string) {
	resp, asJSON := runOp("refs", args, nil)
	locations := resp.Locations
	if asJSON {
		printJSON(orEmptySites(locations))
		return
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
}

func cmdType(args []string) {
	resp, asJSON := runOp("type", args, nil)
	if asJSON {
		printJSON(map[string]any{"hover": resp.Hover})
		return
	}
	if resp.Hover == nil {
		fmt.Fprintln(os.Stderr, "no type information")
		os.Exit(2)
	}
	fmt.Println(stripCodeFences(*resp.Hover))
}

func cmdMembers(args []string) {
	detail := !hasFlag(args, "--no-detail")
	resp, asJSON := runOp("members", args, func(req *ipc.Request) { req.Detail = detail })
	members := resp.Members
	if asJSON {
		printJSON(orEmptyMembers(members))
		return
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
			for _, line := range strings.Split(m.Detail, "\n") {
				fmt.Printf("| %s\n", line)
			}
		}
	}
	if resp.Incomplete {
		fmt.Fprintln(os.Stderr, "(list incomplete — server returned a partial set)")
	}
}

func cmdOutline(args []string) {
	asJSON := hasFlag(args, "--json")
	kind, hasKind := flagValue(args, "--kind")
	pos := positionals(args, []string{"--kind"})
	if len(pos) == 0 {
		fail("usage: idit outline <file> [--kind <kind>] [--json]")
	}
	file := resolveCwd(pos[0])
	sock, server := socketForFile(file)
	resp := sendOp(sock, server.Name, ipc.Request{Op: "outline", File: file})
	if !resp.OK {
		fail("%s", resp.Error)
	}

	tree := resp.Outline
	if asJSON {
		printJSON(orEmptyOutline(tree))
		return
	}
	if hasKind {
		var matches []lsputil.OutlineNode
		for _, n := range flattenOutline(tree) {
			if n.Kind == kind {
				matches = append(matches, n)
			}
		}
		if len(matches) == 0 {
			fmt.Fprintf(os.Stderr, "no %s symbols\n", kind)
			os.Exit(2)
		}
		for _, n := range matches {
			fmt.Printf("%s:%d:%d  %s %s\n", file, n.Line, n.Col, n.Kind, n.Name)
		}
		return
	}
	if len(tree) == 0 {
		fmt.Fprintln(os.Stderr, "no symbols")
		os.Exit(2)
	}
	var sb strings.Builder
	printOutline(&sb, file, tree, 0)
	fmt.Print(sb.String())
}

func cmdSymbol(args []string) {
	asJSON := hasFlag(args, "--json")
	pos := positionals(args, nil)
	if len(pos) == 0 {
		fail("usage: idit symbol <query> [--json]")
	}
	query := pos[0]

	// Project-wide and language-agnostic: query every configured server's daemon
	// and merge the results.
	root := requireRoot()
	cfg, err := workspace.Load(root)
	if err != nil {
		fail("%v", err)
	}
	if len(cfg.Servers) == 0 {
		fail("no servers configured — run `idit server add <name>`")
	}

	var symbols []lsputil.FoundSymbol
	for _, server := range cfg.Servers {
		sock, err := ensureSocket(root, server)
		if err != nil {
			fail("%v", err)
		}
		resp := sendOp(sock, server.Name, ipc.Request{Op: "symbol", Query: query})
		if !resp.OK {
			fail("%s", resp.Error)
		}
		symbols = append(symbols, resp.Symbols...)
	}

	if asJSON {
		printJSON(orEmptyFound(symbols))
		return
	}
	if len(symbols) == 0 {
		fmt.Fprintln(os.Stderr, "no symbols found")
		os.Exit(2)
	}
	sort.SliceStable(symbols, func(i, j int) bool {
		if symbols[i].Name != symbols[j].Name {
			return symbols[i].Name < symbols[j].Name
		}
		if symbols[i].File != symbols[j].File {
			return symbols[i].File < symbols[j].File
		}
		return symbols[i].Line < symbols[j].Line
	})
	for _, s := range symbols {
		container := ""
		if s.Container != "" {
			container = fmt.Sprintf("  (%s)", s.Container)
		}
		fmt.Printf("%s:%d:%d  %s %s%s\n", s.File, s.Line, s.Col, s.Kind, s.Name, container)
	}
}

func cmdCallers(args []string) {
	resp, asJSON := runOp("callers", args, nil)
	callers := resp.Callers
	if asJSON {
		printJSON(orEmptyCallers(callers))
		return
	}
	if len(callers) == 0 {
		fmt.Fprintln(os.Stderr, "no callers found")
		os.Exit(2)
	}
	sort.SliceStable(callers, func(i, j int) bool { return callerLess(callers[i], callers[j]) })
	for _, c := range callers {
		fmt.Printf("%s:%d:%d  %s %s\n", c.File, c.Line, c.Col, c.Kind, c.Name)
	}
}

func cmdCheck(args []string) {
	asJSON := hasFlag(args, "--json")
	fileArg, ok := firstPositional(args)
	if !ok {
		fail("usage: idit check <file> [--json]")
	}
	file := resolveCwd(stripPosition(fileArg))
	sock, server := socketForFile(file)
	resp := sendOp(sock, server.Name, ipc.Request{Op: "check", File: file})
	if !resp.OK {
		fail("%s", resp.Error)
	}

	diags := resp.Diagnostics
	if asJSON {
		printJSON(orEmptyDiags(diags))
		return
	}
	if len(diags) == 0 {
		fmt.Fprintln(os.Stderr, "no problems")
		return // clean → exit 0
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
}

// --- mutation commands ---

func cmdRename(args []string) {
	asJSON := hasFlag(args, "--json")
	dryRun := hasFlag(args, "--dry-run", "-n")
	pos := positionals(args, nil)
	if len(pos) < 2 {
		fail("usage: idit rename <file:line:col> <newName> [--dry-run] [--json]")
	}
	target, err := parseLocator(pos[0])
	if err != nil {
		fail("%v", err)
	}
	newName := pos[1]
	file := resolveCwd(target.File)
	sock, server := socketForFile(file)

	resp := sendOp(sock, server.Name, ipc.Request{
		Op: "rename", File: file, Line: target.Line, Col: target.Col, NewName: newName, DryRun: dryRun,
	})
	if !resp.OK {
		fail("%s", resp.Error)
	}
	if asJSON {
		printJSON(resp)
		return
	}

	verb := "would rename"
	if resp.Applied {
		verb = "renamed"
	}
	fmt.Fprintf(os.Stderr, "%s → '%s': %d edit(s) across %d file(s)\n", verb, newName, len(resp.Sites), resp.FileCount)
	sorted := append([]ipc.EditSite(nil), resp.Sites...)
	sort.SliceStable(sorted, func(i, j int) bool { return editSiteLess(sorted[i], sorted[j]) })
	for _, s := range sorted {
		fmt.Printf("%s:%d:%d\n", s.File, s.Line, s.Col)
	}
	for _, op := range resp.ResourceOps {
		what := op.File
		if op.Kind == "rename" {
			what = fmt.Sprintf("%s -> %s", op.From, op.To)
		}
		fmt.Fprintf(os.Stderr, "  %s: %s\n", op.Kind, what)
	}
}

func cmdMv(args []string) {
	asJSON := hasFlag(args, "--json")
	dryRun := hasFlag(args, "--dry-run", "-n")
	pos := positionals(args, nil)
	if len(pos) < 2 {
		fail("usage: idit mv <from> <to> [--dry-run] [--json]")
	}
	from := resolveCwd(pos[0])
	to := resolveCwd(pos[1])
	// `mv file dir/` → move into the directory, keeping the filename.
	if info, err := os.Stat(to); err == nil && info.IsDir() {
		to = filepath.Join(to, filepath.Base(from))
	}

	sock, server := socketForFile(from)
	resp := sendOp(sock, server.Name, ipc.Request{Op: "mv", From: from, To: to, DryRun: dryRun})
	if !resp.OK {
		fail("%s", resp.Error)
	}
	if asJSON {
		printJSON(resp)
		return
	}

	verb := "would move"
	if resp.Applied {
		verb = "moved"
	}
	fmt.Fprintf(os.Stderr, "%s %s -> %s: %d import edit(s) across %d file(s)\n", verb, from, to, len(resp.Sites), resp.FileCount)
	sorted := append([]ipc.EditSite(nil), resp.Sites...)
	sort.SliceStable(sorted, func(i, j int) bool { return editSiteLess(sorted[i], sorted[j]) })
	for _, s := range sorted {
		fmt.Printf("%s:%d:%d\n", s.File, s.Line, s.Col)
	}
}

func cmdExtract(args []string) {
	asJSON := hasFlag(args, "--json")
	dryRun := hasFlag(args, "--dry-run", "-n")
	scope, _ := flagValue(args, "--scope")
	pos := positionals(args, []string{"--scope"})
	if len(pos) == 0 {
		fail("usage: idit extract <file:line:col-line:col> [--scope <n>] [--dry-run]")
	}
	r, err := parseRange(pos[0])
	if err != nil {
		fail("%v", err)
	}
	file := resolveCwd(r.File)
	sock, server := socketForFile(file)
	resp := sendOp(sock, server.Name, ipc.Request{
		Op: "extract", File: file,
		StartLine: r.StartLine, StartCol: r.StartCol, EndLine: r.EndLine, EndCol: r.EndCol,
		Scope: scope, DryRun: dryRun,
	})
	if asJSON {
		printJSON(resp)
		return
	}
	if !resp.OK {
		fail("%s", resp.Error)
	}

	if resp.Mode == "list" {
		fmt.Fprintln(os.Stderr, "multiple refactorings — pick one with --scope <n>:")
		for _, c := range resp.Candidates {
			fmt.Printf("%d  %s\n", c.Index, c.Title)
		}
		return
	}

	verb := "extracted"
	if resp.Mode == "preview" {
		verb = "would extract"
	}
	fmt.Fprintf(os.Stderr, "%s: %s\n", verb, resp.Chosen)
	for _, s := range resp.Sites {
		fmt.Printf("%s:%d:%d\n", s.File, s.Line, s.Col)
	}
	if p := resp.Placeholder; p != nil {
		fmt.Printf("%s:%d:%d  %s\n", p.File, p.Line, p.Col, p.Name)
		fmt.Fprintf(os.Stderr, "  name it:  idit rename %s:%d:%d <newName>\n", p.File, p.Line, p.Col)
	}
}

// --- small render helpers ---

func siteLess(a, b lsputil.Site) bool {
	if a.File != b.File {
		return a.File < b.File
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Col < b.Col
}

func callerLess(a, b lsputil.CallerSite) bool {
	if a.File != b.File {
		return a.File < b.File
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Col < b.Col
}

func editSiteLess(a, b ipc.EditSite) bool {
	if a.File != b.File {
		return a.File < b.File
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Col < b.Col
}

func nonEmpty(items ...string) []string {
	var out []string
	for _, s := range items {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// collapseWhitespace flattens whitespace around newlines into a single space, so
// a multi-line diagnostic message reads on one line (port of /\s*\n\s*/g → " ").
var newlineWhitespaceRe = regexp.MustCompile(`\s*\n\s*`)

func collapseWhitespace(s string) string {
	return newlineWhitespaceRe.ReplaceAllString(s, " ")
}

// Non-nil coercions so `--json` emits `[]` (not `null`) for empty results.
func orEmptySites(s []lsputil.Site) []lsputil.Site {
	if s == nil {
		return []lsputil.Site{}
	}
	return s
}
func orEmptyMembers(s []lsputil.Member) []lsputil.Member {
	if s == nil {
		return []lsputil.Member{}
	}
	return s
}
func orEmptyOutline(s []lsputil.OutlineNode) []lsputil.OutlineNode {
	if s == nil {
		return []lsputil.OutlineNode{}
	}
	return s
}
func orEmptyFound(s []lsputil.FoundSymbol) []lsputil.FoundSymbol {
	if s == nil {
		return []lsputil.FoundSymbol{}
	}
	return s
}
func orEmptyCallers(s []lsputil.CallerSite) []lsputil.CallerSite {
	if s == nil {
		return []lsputil.CallerSite{}
	}
	return s
}
func orEmptyDiags(s []lsputil.CheckDiagnostic) []lsputil.CheckDiagnostic {
	if s == nil {
		return []lsputil.CheckDiagnostic{}
	}
	return s
}
