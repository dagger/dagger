// This module is a boundary marker, not buildable code.
//
// The repo's Go toolchain (the "golang" dependency) selects what to lint, test
// and generate per Go module. Without a go.mod here, the loose .go files under
// docs/ (mostly older snippets under versioned_docs/ that predate the
// per-snippet module convention) would belong to the repo-root module and be
// pulled into its lint, where they cannot typecheck (they import generated,
// uncommitted dagger code). This go.mod makes all of docs/ a single module so
// the "!docs" selector excludes documentation from the Go toolchain wholesale.
//
// Snippets that need to be standalone, buildable examples still get their own
// nested go.mod + dagger.json; this boundary only catches the rest.
module github.com/dagger/dagger/docs

go 1.26.1
