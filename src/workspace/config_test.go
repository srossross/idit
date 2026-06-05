package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmitLoadRoundTrip(t *testing.T) {
	cfg := IditConfig{Servers: []ServerConfig{
		presetServer("tsserver"),
		presetServer("gopls"),
	}}
	out, err := Emit(cfg)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}

	root := t.TempDir()
	writeConfig(t, root, out)

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Servers) != 2 {
		t.Fatalf("want 2 servers, got %d", len(got.Servers))
	}
	// Order preserved: tsserver before gopls.
	if got.Servers[0].Name != "tsserver" || got.Servers[1].Name != "gopls" {
		t.Fatalf("order not preserved: %v", []string{got.Servers[0].Name, got.Servers[1].Name})
	}
	ts := got.Servers[0]
	if ts.LanguageID != "typescript" || ts.Command[0] != "typescript-language-server" {
		t.Fatalf("tsserver fields wrong: %+v", ts)
	}
	if ts.Extensions[0] != ".ts" {
		t.Fatalf("extensions wrong: %v", ts.Extensions)
	}
}

func TestEmitLoadSettingsRoundTrip(t *testing.T) {
	bp := presetServer("basedpyright")
	bp.Settings = map[string]map[string]any{
		"python": {"pythonPath": "/ws/.venv/bin/python"},
	}
	out, err := Emit(IditConfig{Servers: []ServerConfig{bp}})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	root := t.TempDir()
	writeConfig(t, root, out)

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := got.Servers[0]
	if s.InterpreterPath() != "/ws/.venv/bin/python" {
		t.Fatalf("pythonPath not round-tripped: %+v", s.Settings)
	}
	if v := s.LSPSettings()["python"]; v == nil {
		t.Fatalf("LSPSettings missing python section: %v", s.LSPSettings())
	}
}

func TestSqlsConnectionConfigRoundTrip(t *testing.T) {
	sqls := presetServer("sqls")
	// sqls reads its DB connection from the "sqls" section's connections array,
	// requested via workspace/configuration — the same path Settings serves.
	sqls.Settings = map[string]map[string]any{
		"sqls": {"connections": []any{
			map[string]any{"driver": "postgresql", "dataSourceName": "host=127.0.0.1 user=app dbname=app sslmode=disable"},
		}},
	}
	out, err := Emit(IditConfig{Servers: []ServerConfig{sqls}})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	root := t.TempDir()
	writeConfig(t, root, out)

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := got.Servers[0]
	if mapped, ok := ServerForFile(got, "/db/schema.sql"); !ok || mapped.Name != "sqls" {
		t.Fatalf(".sql should map to sqls, got %q ok=%v", mapped.Name, ok)
	}
	// LSPSettings()["sqls"] is exactly what the workspace/configuration handler
	// returns to sqls; the connection must survive the YAML round-trip.
	sec, ok := s.LSPSettings()["sqls"].(map[string]any)
	if !ok {
		t.Fatalf("LSPSettings missing sqls section: %v", s.LSPSettings())
	}
	conns, ok := sec["connections"].([]any)
	if !ok || len(conns) != 1 {
		t.Fatalf("connections not round-tripped: %#v", sec["connections"])
	}
	conn := conns[0].(map[string]any)
	if conn["driver"] != "postgresql" || conn["dataSourceName"] == "" {
		t.Fatalf("connection fields lost: %#v", conn)
	}
}

func TestValidateRuntime(t *testing.T) {
	// A Python server with no interpreter fails fast.
	bp := presetServer("basedpyright")
	if err := bp.ValidateRuntime(); err == nil {
		t.Fatal("python server with no interpreter should fail validation")
	}
	// A non-existent configured path also fails.
	bp.Settings = map[string]map[string]any{"python": {"pythonPath": "/no/such/python"}}
	if err := bp.ValidateRuntime(); err == nil {
		t.Fatal("non-existent interpreter path should fail validation")
	}
	// A real, existing file passes.
	f := filepath.Join(t.TempDir(), "python")
	if err := os.WriteFile(f, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bp.Settings["python"]["pythonPath"] = f
	if err := bp.ValidateRuntime(); err != nil {
		t.Fatalf("valid interpreter should pass: %v", err)
	}
	// Non-Python servers never require an interpreter.
	if err := presetServer("gopls").ValidateRuntime(); err != nil {
		t.Fatalf("gopls should not require an interpreter: %v", err)
	}
}

func TestCheckBinary(t *testing.T) {
	// A preset whose binary is absent reports a guiding error with the hint.
	s := presetServer("basedpyright")
	s.Command = []string{"idit-definitely-not-a-real-binary-xyz"}
	err := s.CheckBinary()
	if err == nil {
		t.Fatal("missing binary should error")
	}
	if !strings.Contains(err.Error(), "uv tool install basedpyright") {
		t.Fatalf("error should include the install hint: %v", err)
	}
	// A binary that is on PATH passes (go drives these tests, so it's present).
	s.Command = []string{"go"}
	if err := s.CheckBinary(); err != nil {
		t.Fatalf("go should resolve on PATH: %v", err)
	}
}

func TestProjectSetupHintClangd(t *testing.T) {
	root := t.TempDir()
	clangd := presetServer("clangd")
	if clangd.ProjectSetupHint(root) == "" {
		t.Fatal("clangd with no compilation database should produce a hint")
	}
	if !strings.Contains(clangd.ProjectSetupHint(root), "CMAKE_EXPORT_COMPILE_COMMANDS") {
		t.Fatalf("hint should name the cmake flag: %q", clangd.ProjectSetupHint(root))
	}
	// A compile_commands.json clears it.
	if err := os.WriteFile(filepath.Join(root, "compile_commands.json"), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if h := clangd.ProjectSetupHint(root); h != "" {
		t.Fatalf("compile DB present should clear the hint, got %q", h)
	}
	// Non-C++ servers never get the clangd hint.
	if presetServer("gopls").ProjectSetupHint(root) != "" {
		t.Fatal("gopls should have no clangd setup hint")
	}
}

func TestDetectPythonInterpreter(t *testing.T) {
	t.Setenv("VIRTUAL_ENV", "") // isolate from the test runner's active venv
	root := t.TempDir()
	if got := DetectPythonInterpreter(root); got != "" {
		t.Fatalf("empty project should detect nothing, got %q", got)
	}
	bin := filepath.Join(root, ".venv", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	py := filepath.Join(bin, "python")
	if err := os.WriteFile(py, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DetectPythonInterpreter(root); got != py {
		t.Fatalf("want %q, got %q", py, got)
	}
}

func presetServer(name string) ServerConfig {
	p := Presets[name]
	p.Name = name
	return p
}

// writeConfig creates the state dir under root and writes data as the config.
func writeConfig(t *testing.T, root string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, StateDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath(root), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil || len(cfg.Servers) != 0 {
		t.Fatalf("missing file should be empty config: %+v err=%v", cfg, err)
	}
}

func TestLoadLowercasesExtensions(t *testing.T) {
	root := t.TempDir()
	yaml := "servers:\n  custom:\n    kind: lsp\n    command:\n      - foo\n    extensions:\n      - .TS\n      - .JSX\n    languageId: x\n"
	writeConfig(t, root, []byte(yaml))
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Servers[0].Extensions[0] != ".ts" || cfg.Servers[0].Extensions[1] != ".jsx" {
		t.Fatalf("extensions not lowercased: %v", cfg.Servers[0].Extensions)
	}
}

func TestLoadRejectsBadKind(t *testing.T) {
	root := t.TempDir()
	yaml := "servers:\n  x:\n    kind: dap\n    command: [a]\n    extensions: [.a]\n"
	writeConfig(t, root, []byte(yaml))
	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "unsupported kind") {
		t.Fatalf("want unsupported-kind error, got %v", err)
	}
}

func TestLoadDefaultsLanguageIDToName(t *testing.T) {
	root := t.TempDir()
	yaml := "servers:\n  myserver:\n    command: [a]\n    extensions: [.a]\n"
	writeConfig(t, root, []byte(yaml))
	cfg, _ := Load(root)
	if cfg.Servers[0].LanguageID != "myserver" {
		t.Fatalf("languageId default wrong: %q", cfg.Servers[0].LanguageID)
	}
}

func TestServerForFile(t *testing.T) {
	cfg := IditConfig{Servers: []ServerConfig{presetServer("tsserver"), presetServer("gopls")}}
	s, ok := ServerForFile(cfg, "/x/y/main.go")
	if !ok || s.Name != "gopls" {
		t.Fatalf("go file → gopls, got %v ok=%v", s.Name, ok)
	}
	s, ok = ServerForFile(cfg, "/x/Foo.TSX")
	if !ok || s.Name != "tsserver" {
		t.Fatalf("tsx (uppercase) → tsserver, got %v ok=%v", s.Name, ok)
	}
	if _, ok := ServerForFile(cfg, "/x/README"); ok {
		t.Fatal("no extension should not match")
	}

	py := IditConfig{Servers: []ServerConfig{presetServer("basedpyright")}}
	s, ok = ServerForFile(py, "/x/y/main.py")
	if !ok || s.Name != "basedpyright" || s.LanguageID != "python" {
		t.Fatalf("py file → basedpyright/python, got %v ok=%v langID=%q", s.Name, ok, s.LanguageID)
	}
}

func TestFindRoot(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(filepath.Join(root, StateDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	got, ok := FindRoot(deep)
	if !ok || got != root {
		t.Fatalf("FindRoot walked wrong: got %q ok=%v want %q", got, ok, root)
	}
}
