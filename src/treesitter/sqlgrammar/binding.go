// Package sqlgrammar is the vendored DerekStride/tree-sitter-sql grammar (MIT,
// see LICENSE). It is vendored rather than imported as a module because that
// module ships only scanner.c — the generated parser.c (17 MB) and its headers
// are gitignored upstream, so a plain `go get` fails to compile. The generated
// sources here come from the npm release (@derekstride/tree-sitter-sql@0.3.11),
// which bundles them. Grammar ABI is 14, accepted by go-tree-sitter v0.24.0.
//
// cgo compiles parser.c and scanner.c automatically (they sit in this package
// directory), so this file only declares the entry point — it must not #include
// the .c files, or their symbols would be defined twice and fail to link.
package sqlgrammar

// #cgo CFLAGS: -std=c11 -fPIC
// #include "tree_sitter/parser.h"
// const TSLanguage *tree_sitter_sql(void);
import "C"

import "unsafe"

// Language returns the tree-sitter language pointer for SQL.
func Language() unsafe.Pointer {
	return unsafe.Pointer(C.tree_sitter_sql())
}
