# idit

A language-server CLI for searching, navigating, and editing code semantically
from the terminal. `idit` combines two layers:

- **Tree-sitter** (compiled into the binary): backs `idit find --kind …` and
  `idit locate …`. No external tools needed.
- **LSP**: backs the semantic commands (`def`, `refs`, `rename`, `check`,
  `outline`, `symbol`, `members`, `type`, `callers`) by talking to a per-language
  language server.

## CLI principles

- **Guide the user, fail fast.** Misconfiguration (missing server binary, no
  Python interpreter, an over-long socket path) errors immediately with an
  actionable message — never a silent hang.
- **No magic unless asked.** `idit` does not auto-install servers or
  auto-detect interpreters at query time. You opt in explicitly.

## Install

Build the CLI (CGo is required — a C compiler must be on PATH):

```bash
go install github.com/srossross/clidit/cmd/idit@latest
```

## Language servers are not bundled

`idit` runs language servers; it does **not** install them. You install the
server for your language once, then point `idit` at it. Each preset knows the
install command — `idit server list` shows it, and `idit` tells you if a
configured server's binary is missing.

| Language      | Preset         | Install the server with                                |
| ------------- | -------------- | ------------------------------------------------------ |
| TypeScript/JS | `tsserver`     | `npm install -g typescript-language-server typescript` |
| Go            | `gopls`        | `go install golang.org/x/tools/gopls@latest`           |
| C / C++       | `clangd`       | `brew install llvm` / `apt install clangd`             |
| Python        | `basedpyright` | `uv tool install basedpyright` (or `npm install -g basedpyright`) |

## Getting started

```bash
# 1. install the language server for your project (see the table above), e.g.:
uv tool install basedpyright

# 2. create a workspace and add the server
idit init .
idit server add basedpyright          # warns if the binary isn't on PATH yet

# 3. use it
idit outline path/to/file.py
idit def path/to/file.py:LINE:COL
```

### Python: the interpreter

`basedpyright` resolves imports and types through a Python interpreter (your
project's virtualenv). Point it at one of two ways:

- **Auto (opt-in):** create a venv at the project root, then add with
  `--auto-config`, which detects `$VIRTUAL_ENV`, `./.venv`, or `./venv` and
  writes `python.pythonPath` into `.idit/config.yml`:

  ```bash
  uv venv
  idit server add basedpyright --auto-config
  ```

- **Manual:** set `settings.python.pythonPath` for the server in
  `.idit/config.yml`.

If no interpreter is configured, `idit` fails fast and tells you how to fix it.

## Workspace layout

`idit init` creates a `.idit/` directory holding:

- `config.yml` — the configured servers
- `<server>.sock`, `<server>.pid` — the running daemon's socket and pid
- `logs/<server>.log` — daemon logs

## Build requirements

- **CGo** (`CGO_ENABLED=1`, the default) for the bundled tree-sitter grammars.
- Cross-compiling needs a cross C toolchain (e.g. `zig cc`), not just
  `GOOS`/`GOARCH`.
