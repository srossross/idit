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
