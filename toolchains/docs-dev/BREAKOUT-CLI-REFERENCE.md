# Break Out CLI Reference Generation

## Summary

Move CLI reference generation out of the hidden `dagger gen` command and into a reusable Cobra docs generator.

Keep repo-specific orchestration in `toolchains/docs-dev`, but make the CLI docs artifact come from a normal Go/Cobra generation path.

## Current State

Today:

```text
toolchains/docs-dev.References()
  -> toolchains/cli-dev.Reference()
    -> go run ./cmd/dagger gen --output cli.mdx --include-experimental
      -> cmd/dagger/gen.go
        -> github.com/spf13/cobra/doc
```

`cmd/dagger/gen.go` is a hidden production CLI command whose only real job is docs generation.

## Proposal

1. Move the Dagger CLI command tree into an importable internal package:

```text
internal/daggercli/
  root.go
  docs_generate.go

cmd/dagger/
  main.go
```

`cmd/dagger/main.go` should be a thin executable wrapper.

`internal/daggercli` should expose:

```go
func NewRootCommand(opts Options) *cobra.Command
```

2. Add a source-adjacent generation declaration:

```text
internal/daggercli/docs_generate.go
```

Example shape:

```go
//go:build generate

package daggercli

//go:generate go run github.com/dagger/cobra/cmd/cobra-docs@v0.1.0 -root NewRootCommand -out ../../docs/current_docs/reference/cli/index.mdx
```

The exact flags are part of the proposed `github.com/dagger/cobra` contract.

3. Create or use a reusable `github.com/dagger/cobra` module/tool.

It wraps Cobra's official docs package:

```go
github.com/spf13/cobra/doc
```

The module/tool owns:

```text
markdown/man/yaml/rst rendering
single-file vs tree output
frontmatter
link handling
hidden/experimental filtering hooks
```

The project owns:

```text
how to construct the root command
which output path to write
project-specific command filtering policy
```

## Dagger Flow

After installing the reusable module, the desired user flow is:

```text
dagger generate
git add .
git commit -s -m "re-generate all CLI docs"
```

The installed Cobra module should discover Cobra docs generation declarations and run them in a Go environment, likely reusing `github.com/dagger/go` for Go module setup and caching.

`toolchains/docs-dev.References()` should stop knowing how CLI docs are generated. It should only merge the generated CLI reference into the same final docs changeset as the GraphQL and JSON schema references.

## Why `internal/daggercli`

Use:

```text
internal/daggercli
```

not:

```text
cmd/dagger/internal/app
```

because generators and tests elsewhere in this repo may need to import the command constructor. Go's `internal` visibility for `cmd/dagger/internal/app` would only allow imports from `cmd/dagger/...`.

`internal/daggercli` remains private to this repo while being available to repo-local generators.

## Migration

1. Move command construction from `cmd/dagger` to `internal/daggercli`.
2. Keep `cmd/dagger/main.go` as the executable entrypoint.
3. Add `internal/daggercli/docs_generate.go`.
4. Replace `toolchains/cli-dev.Reference()` so it no longer calls `dagger gen`.
5. Delete `cmd/dagger/gen.go`.
6. Keep `toolchains/docs-dev.References()` as the repo-specific orchestrator.

## Non-Goals

- Do not make `docs-dev` project-agnostic.
- Do not require a Dagger-only binary in `//go:generate`.
- Do not rely on static scanning to guess a Cobra root command.
- Do not keep docs generation as a hidden production CLI command.
