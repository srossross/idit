

idit cli principles:
* Guide user + fail fast
* No magic unless explicitly asked for


* run with `go run`
* dont use `go build` without puropse
* use `golangci-lint run --fix` to lint vet etc
* use `golangci-lint fmt` fix formatting issues

## Build requirements

* **CGo is required** (`CGO_ENABLED=1`, the default) — the `src/treesitter` package
  links the tree-sitter C runtime + bundled grammars for `idit find --kind`/`idit locate`.
  A C compiler must be on PATH (clang on macOS via Xcode CLT, gcc/clang on Linux).
* Cross-compiling therefore needs a cross C toolchain (e.g. `zig cc`), not just
  `GOOS`/`GOARCH`.
* tree-sitter grammar modules are pinned to a language ABI the runtime accepts
  (runtime `go-tree-sitter v0.24.0` → grammars `v0.23.x`). The
  `TestRegistryKindsCompile` test in `src/treesitter` fails loudly if a version
  bump breaks ABI compatibility.

