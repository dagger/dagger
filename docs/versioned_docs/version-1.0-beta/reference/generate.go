// Package clidocs hosts the go:generate directive that renders the CLI
// reference. It has no buildable code; the directive runs the docsgen tool
// from the repository root so the output lands in this module's own tree.
package clidocs

// Mount the generated reference into this module's generate source so the
// toolchain diffs the regenerated output against the committed file.
//go:generate:include cli/**

//go:generate go -C ../../../ run ./internal/cmd/dagger/docsgen -out docs/current_docs/reference/cli/index.mdx -include-experimental
