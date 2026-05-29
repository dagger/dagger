# CLI Reference Breakout Implementation Plan

## Target Outcome

CLI reference generation should be a normal Cobra docs generation path, not a hidden
production CLI command.

The final shape I want:

```text
cmd/dagger/
  main.go                 # executable wrapper only

internal/cmd/dagger/
  root.go                 # importable command constructor
  docs_generate.go        # source-adjacent generation declaration
  docs_frontmatter.mdx
  docsgen/                # private go:generate driver
  ...

internal/cobradocs/
  markdown.go             # reusable Cobra docs rendering package
```

The Dagger repo owns how the `dagger` command is constructed and where its generated
reference lives. `internal/cobradocs` owns the generic rendering mechanics, but the
Dagger-specific generator driver stays under `internal/cmd/dagger` so we do not
publish or imply a stable `dagger-docs` command.

## Decisions

- Use `internal/cmd/dagger`, not `internal/daggercli`.
  This keeps the pattern open for other `cmd/*` binaries while still making the
  Dagger CLI implementation importable inside this repo.
- Use package name `daggercmd`.
  The import reads cleanly from the wrapper:

  ```go
  import daggercmd "github.com/dagger/dagger/internal/cmd/dagger"
  ```

- Keep `cmd/dagger/main.go` as a thin executable wrapper.
  It should delegate process execution to the internal package and contain no
  command-tree construction.
- Delete `cmd/dagger/gen.go`.
  There should be no hidden `dagger gen` command and no compatibility alias.
- Keep the docs generator private.
  Do not add a top-level `cmd/dagger-docs` binary; a `cmd/*` package looks like
  user-facing executable surface. Use `internal/cmd/dagger/docsgen` as a local
  `go generate` driver instead.
- The reference generator must construct the Cobra root directly through:

  ```go
  func NewRootCommand(opts Options) *cobra.Command
  ```

- The generated output remains:

  ```text
  docs/current_docs/reference/cli/index.mdx
  ```

## Current State

Today the flow is:

```text
toolchains/docs-dev.References()
  -> toolchains/cli-dev.Reference()
    -> go run ./cmd/dagger gen --output cli.mdx --include-experimental
      -> cmd/dagger/gen.go
        -> github.com/spf13/cobra/doc
```

`cmd/dagger/gen.go` is production CLI surface whose only job is documentation.
That is the wrong ownership boundary.

## Implementation Plan

### 1. Move the CLI implementation package

Move the current `cmd/dagger` implementation files into:

```text
internal/cmd/dagger
```

This includes embedded assets such as:

```text
licenses/Apache-2.0.txt
*.graphql
llm_compact.md
testdata/
```

The remaining `cmd/dagger/main.go` should call into `daggercmd` only.

### 2. Introduce the command constructor

Add:

```go
type Options struct {
    Args   []string
    Stdin  io.Reader
    Stdout io.Writer
    Stderr io.Writer
}

func NewRootCommand(opts Options) *cobra.Command
```

The first pass should preserve the CLI's existing runtime behavior, but command
allocation and command registration should live behind this constructor instead of
package `init()` wiring. The goal is for docs generation and tests to import the
command tree without importing an executable package.

Important detail: current command state is heavily process-global. I will avoid a
cosmetic full state rewrite unless the move proves it is necessary. The required
hard cutover is the package boundary and docs generation ownership, not making the
entire CLI multi-instance-safe in one patch.

### 3. Keep executable behavior in one internal entrypoint

Add an internal process entrypoint, for example:

```go
func Main()
```

`cmd/dagger/main.go` becomes:

```go
package main

import daggercmd "github.com/dagger/dagger/internal/cmd/dagger"

func main() {
    daggercmd.Main()
}
```

The current exit-code handling, progress setup, signal handling, and global flag
preparse behavior should move intact into `daggercmd.Main()`.

### 4. Add source-adjacent docs generation declaration

Add:

```text
internal/cmd/dagger/docs_generate.go
internal/cmd/dagger/docs_frontmatter.mdx
internal/cmd/dagger/docsgen/main.go
```

Expected shape:

```go
//go:build generate

package daggercmd

//go:generate go run ./docsgen -out ../../../docs/current_docs/reference/cli/index.mdx -frontmatter docs_frontmatter.mdx -include-experimental
```

`go generate` runs from the package directory, so `./docsgen` remains source-local
and private to the CLI implementation.

The local driver should be thin. It should import `daggercmd`, call
`NewRootCommand`, apply Dagger-specific filtering policy, and pass the command tree
to `internal/cobradocs`.

`internal/cobradocs` needs to support at least:

- Markdown single-file output
- output path
- frontmatter from file
- same-document Cobra links
- hidden command handling
- command filtering hooks

### 5. Move docs rendering behavior out of Dagger CLI

The behavior currently in `cmd/dagger/gen.go` should move out of the production
CLI. Generic rendering mechanics belong in `internal/cobradocs`; Dagger-specific
construction and filtering belong in `internal/cmd/dagger/docsgen`.

Behavior to preserve:

- disable Cobra's autogenerated footer
- prepend CLI docs frontmatter
- render one Markdown document containing the root and all available subcommands
- link `SEE ALSO` entries to same-file anchors
- omit experimental commands unless explicitly included by the driver
- omit `dagger completion` in the driver because its current long text breaks
  Docusaurus MDX

### 6. Update docs-dev orchestration

Remove the CLI reference generation edge from:

```go
toolchains/docs-dev.References()
```

Specifically, stop calling:

```go
dag.DaggerCli().Reference(...)
```

`docs-dev` should remain repo-specific orchestration for the docs changeset, but it
should not know that CLI docs used to be generated by running the Dagger CLI.

### 7. Remove cli-dev reference generation

Delete or simplify:

```go
toolchains/cli-dev.Reference()
```

No Dagger module should call `go run ./cmd/dagger gen`. If generated bindings need
to be updated after removing the method, regenerate the affected Dagger SDK files.

### 8. Delete hidden CLI generation command

Delete:

```text
cmd/dagger/gen.go
```

After this, `dagger gen` should not exist.

## Verification Plan

Use the current generated docs as the behavioral baseline.

1. Before removing the old command, capture current output to `/tmp/cli-reference-old.mdx`.
2. Generate through `go generate ./internal/cmd/dagger` or `go run ./docsgen`
   from `internal/cmd/dagger` to `/tmp/cli-reference-new.mdx`.
3. Diff both files. Expected differences should be zero, except for changes we
   deliberately make to frontmatter placement or generator metadata.
4. Compile the executable wrapper and internal command package.
5. Run focused tests for command construction, completion, and docs generation.
6. Run the repo generator flow that is expected to own the final artifact:

   ```text
   dagger generate
   ```

7. Confirm `docs/current_docs/reference/cli/index.mdx` is updated only by the new
   generation path.

## Future Extraction

If another repository needs the same Cobra docs generator, extract
`internal/cobradocs` into a public module at that point. Do not design a public
`github.com/dagger/cobra` contract until there is a second real consumer.

## Non-Goals

- Do not keep `dagger gen`.
- Do not add a top-level `cmd/dagger-docs` command.
- Do not make `docs-dev` project-agnostic.
- Do not infer the Cobra root by static scanning.
- Do not promise a stable external Cobra docs CLI or module yet.
- Do not refactor unrelated CLI runtime state just because the package is moving.
