// Command wire-codegen generates the TypeScript wire surface and the Go path
// constants from the single wire contract in internal/wirespec, using the
// wiregen library (AST-based; github.com/cplieger/wiregen/v2).
//
// Outputs:
//   - internal/server/static-src/wire/{types,decoders,client}.gen.ts —
//     interfaces, validating decoders, and the typed decoder-bound client
//     (consumed directly by the client modules; api-types.ts re-exports the
//     common surface).
//   - internal/apipaths/paths.gen.go — Path* constants shared by the CLI.
//
// The endpoint table's auth groups are checked against routes.go by
// internal/server's wirespec consistency test; routes.go stays authoritative
// for permissions.
//
// Run: go run ./cmd/wire-codegen   (from the subflux repo root)
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cplieger/subflux/internal/wirespec"
)

func main() {
	r := wirespec.Registry()

	outDir := filepath.Join("internal", "server", "static-src", "wire")
	if err := r.Generate(outDir); err != nil {
		fmt.Fprintf(os.Stderr, "wire-codegen: %v\n", err)
		os.Exit(1)
	}

	pathsDir := filepath.Join("internal", "apipaths")
	if err := os.MkdirAll(pathsDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "wire-codegen: mkdir %s: %v\n", pathsDir, err)
		os.Exit(1)
	}
	goPaths, err := r.GenerateGoPaths("apipaths")
	if err != nil {
		fmt.Fprintf(os.Stderr, "wire-codegen: %v\n", err)
		os.Exit(1)
	}
	pathsFile := filepath.Join(pathsDir, "paths.gen.go")
	if err := os.WriteFile(pathsFile, []byte(goPaths), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "wire-codegen: write %s: %v\n", pathsFile, err)
		os.Exit(1)
	}

	fmt.Println("wire-codegen: generated " + outDir + "/{types,decoders,client}.gen.ts and " + pathsFile)
}
