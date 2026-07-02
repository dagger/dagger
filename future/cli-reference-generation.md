# Future CLI Reference Generation

author: shykes
created: 2026-06-15
status: future task
related: future/spin-out-generated-clients.md

## Goal

Generate `docs/current_docs/reference/cli/index.mdx` through the standard
`dagger generate` / `dagger check` toolchain, instead of a hidden `dagger gen`
command driven by a bespoke `cli-dev` / `docs-dev` pipeline.

## Context

Today:

```text
docs-dev.References() -> cli-dev.Reference()
  -> go run ./cmd/dagger gen --output cli.mdx
    -> cmd/dagger/gen.go (hidden command) -> spf13/cobra/doc
```

Two problems:

1. Generation logic ships as a hidden command in the production binary.
2. The reference is produced and checked by a project-specific pipeline rather
   than the shared generation machinery every other generated file already uses.

CLI reference only. GraphQL and JSON-schema breakouts are out of scope.

## Non-Goals

Do not touch the ~23 `init()` functions that wire the command tree. The
generator needs the *assembled* tree, not a new construction mechanism. A prior
attempt rewrote them into `setupXxx()` calls — pure churn, large conflicts.
Avoid that.

## Target Shape

1. Move `cmd/dagger/*.go` into `internal/cmd/dagger` (`package daggercmd`),
   `init()` kept as-is; leave `cmd/dagger/main.go` a thin wrapper. Per-file
   change is just the package line + relocation.

2. Expose `func RootCommand() *cobra.Command` (runs the pre-`Execute()` setup so
   global flags survive) and `func IsExperimental(*cobra.Command) bool`.

3. Add `internal/cobradocs` (reusable Cobra markdown walker) and
   `internal/cmd/dagger/docsgen` (imports `RootCommand()`, hides experimental +
   `completion`, embeds frontmatter) — same output as `gen.go`.

4. Drive it from the docs tree itself: a thin `go:generate` module at
   `docs/current_docs/reference` runs `docsgen` from the repo root so the
   reference lands in that module's own tree. A `//go:generate:include cli/**`
   directive mounts the committed reference into the generate source, so the
   regenerated output diffs against it and a current file yields an empty,
   check-clean changeset.

5. Run it through the shared `github.com/dagger/go` toolchain: `dagger.json`
   restricts that toolchain's `generate` selection to
   `docs/current_docs/reference` (and keeps `test` at `e2e/**`, `lint` off).
   `dagger generate` produces the reference; `dagger check` verifies freshness.

6. Delete `gen.go`; drop `cli-dev.Reference()` and the `docs-dev` CLI-reference
   step; regenerate bindings.

## Done Criteria

- Reference is produced by `dagger generate` and verified by `dagger check`,
  output equivalent to today's.
- No project-specific generation plumbing: `gen.go` / hidden `dagger gen`,
  `cli-dev.Reference()`, and the `docs-dev` CLI-reference step are all gone.
- `init()` registration unchanged from upstream.
