# Go SDK: skip codegen at runtime

## Status: Proposed (stacked on PR 1 `no-codegen-at-runtime`)

This design extends the broader "no codegen at runtime" goal by letting Go
SDK modules commit their generated code (`dagger.gen.go` and
`internal/dagger/**`) and skip the `codegen generate-module` subprocess
during runtime operations (`dagger call`, `dagger functions`, etc.).
Codegen still runs during `dagger init` and `dagger develop`.

PR 1 reshaped the codegen/typedef boundary so the SDK owns Phase 1 + 2 of
module-type discovery. PR 2 (this design) takes the next step: for Go
modules that opt in, the engine stops invoking the codegen binary
entirely during runtime, trimming the hot path from
*codegen → go build → exec* to *go build → exec*.

## Goals

- Let Go modules opt out of runtime codegen via `dagger.json`.
- Preserve full backwards compatibility: existing modules see zero
  behavior change.
- New Go modules default to the opt-in path so the "legacy" runtime
  codegen call starts shrinking for new users.
- Keep the opt-in guardrails explicit: skipping runtime codegen *requires*
  the generated files to live in the repo, so `automaticGitignore=true`
  (which would suppress them) is rejected.

## Non-goals

- Changing non-Go SDKs. Python, TypeScript, etc. read the new field but
  ignore it; their runtime flow is unchanged.
- Detecting that committed `dagger.gen.go` drifted from the engine schema
  (stale-generated-code detection). Users are trusted; worst case they
  get a clean error from the binary and rerun `dagger develop`.
- Committing the compiled `/runtime` binary. `go build` still runs inside
  the runtime container.

## Current state

`core/modules/config.go:283` defines:

```go
type ModuleCodegenConfig struct {
    AutomaticGitignore *bool `json:"automaticGitignore,omitempty"`
}
```

Go SDK's runtime path (`core/sdk/go_sdk.go`):

- `Runtime()` calls `baseWithCodegen` which:
  - Mounts the introspection JSON.
  - Strips `dagger.gen.go` from the context dir via `withoutFile`.
  - Runs `codegen generate-module` inside a privileged container.
  - Mounts the result; subsequent `go build` compiles it.
- `Codegen()` calls the same `baseWithCodegen`, then exports the
  generated directory. This is what `dagger develop` uses.

So today, every `dagger call` re-runs codegen. That's the overhead this
design eliminates.

## Target state

```
dagger.json:
  codegen:
    automaticGitignore: false
    legacyCodegenAtRuntime: false

dagger call foo (Go SDK, new mode):
  Runtime()
    ├─ read CodegenConfig.LegacyCodegenAtRuntime → false
    ├─ mount user context dir as-is (dagger.gen.go + internal/dagger/ included)
    ├─ verify dagger.gen.go + internal/dagger/dagger.gen.go exist → ok
    ├─ go build -o /runtime .
    └─ withEntrypoint(/runtime)
```

No `codegen generate-module` invocation. No introspection JSON mount. No
`withoutFile(dagger.gen.go)`.

## Config surface

### Field

Extend `core/modules/config.go`:

```go
type ModuleCodegenConfig struct {
    // Whether to automatically generate a .gitignore file for this module.
    AutomaticGitignore *bool `json:"automaticGitignore,omitempty"`

    // LegacyCodegenAtRuntime controls whether the SDK runs codegen during
    // runtime operations (dagger call, dagger functions, etc.). When
    // explicitly false, the SDK trusts the committed generated files and
    // skips the runtime codegen pass entirely. Codegen still runs on
    // dagger init and dagger develop.
    //
    // Currently honored only by the Go SDK; other SDKs read but ignore
    // this field.
    //
    // Default (nil or true): run codegen at runtime (legacy behavior).
    LegacyCodegenAtRuntime *bool `json:"legacyCodegenAtRuntime,omitempty"`
}
```

Both fields remain `*bool` to distinguish "user didn't set" from "user set
to false".

### Semantics

| `LegacyCodegenAtRuntime` | Behavior |
|---|---|
| `nil` (field absent) | Legacy: codegen runs on every runtime call. |
| `true` | Legacy (explicit). Same as `nil`. |
| `false` | New: Go SDK's `Runtime()` skips `codegen generate-module`. |

### Validation

Add `ModuleCodegenConfig.Validate()` (or extend existing validation in
`core/schema/modulesource.go`):

```go
func (c *ModuleCodegenConfig) Validate() error {
    if c == nil {
        return nil
    }
    if c.LegacyCodegenAtRuntime != nil && !*c.LegacyCodegenAtRuntime {
        // Opting out of runtime codegen requires committing generated files.
        if c.AutomaticGitignore == nil || *c.AutomaticGitignore {
            return fmt.Errorf(
                "codegen.legacyCodegenAtRuntime=false requires " +
                "codegen.automaticGitignore=false " +
                "(generated files must be committed to the repo)")
        }
    }
    return nil
}
```

Validation runs at module load, so the error surfaces before any codegen
or container work.

## Go SDK runtime path

`core/sdk/go_sdk.go` today has `baseWithCodegen` used by both `Runtime()`
and `Codegen()`. We split them along the new axis.

### Helper

```go
// useRuntimeCodegen reports whether the module wants the SDK to run
// codegen during runtime operations (dagger call, dagger functions).
// True for modules that haven't opted into the new mode.
func useRuntimeCodegen(src *core.ModuleSource) bool {
    c := src.CodegenConfig
    if c == nil || c.LegacyCodegenAtRuntime == nil {
        return true
    }
    return *c.LegacyCodegenAtRuntime
}
```

### Split

- `Codegen()` continues to call the codegen-running path unconditionally.
  This is what `dagger develop` exercises; skipping codegen there would
  defeat the point.
- `Runtime()` branches on `useRuntimeCodegen(src)`:
  - `true` → existing path (rename internal helper to
    `baseWithRuntimeCodegen` for clarity).
  - `false` → new path `baseForCommittedCodegen`.

### `baseForCommittedCodegen`

```go
func (sdk *goSDK) baseForCommittedCodegen(
    ctx context.Context,
    src dagql.ObjectResult[*core.ModuleSource],
) (dagql.ObjectResult[*core.Container], error) {
    dag, err := sdk.root.Server.Server(ctx)
    if err != nil {
        return dagql.ObjectResult[*core.Container]{}, err
    }

    contextDir := src.Self().ContextDirectory
    srcSubpath := src.Self().SourceSubpath

    // Verify the two generated-file markers exist. If either is missing,
    // the module cannot build — point the user at the fix.
    if err := requireGeneratedFiles(ctx, dag, contextDir, srcSubpath); err != nil {
        return dagql.ObjectResult[*core.Container]{}, err
    }

    ctr, err := sdk.base(ctx)
    if err != nil {
        return dagql.ObjectResult[*core.Container]{}, err
    }

    // Mount the user context as-is — no codegen, no withoutFile, no
    // schema JSON. `go build` (done by the caller in Runtime()) compiles
    // whatever the user committed.
    if err := dag.Select(ctx, ctr, &ctr,
        dagql.Selector{
            Field: "withMountedDirectory",
            Args: []dagql.NamedInput{
                {Name: "path", Value: dagql.NewString(goSDKUserModContextDirPath)},
                {Name: "source", Value: dagql.NewID[*core.Directory](contextDir.ID())},
            },
        },
        dagql.Selector{
            Field: "withWorkdir",
            Args: []dagql.NamedInput{
                {Name: "path", Value: dagql.NewString(
                    filepath.Join(goSDKUserModContextDirPath, srcSubpath))},
            },
        },
    ); err != nil {
        return ctr, fmt.Errorf("mount module source: %w", err)
    }
    return ctr, nil
}
```

`requireGeneratedFiles` checks for:

- `<srcSubpath>/dagger.gen.go`
- `<srcSubpath>/internal/dagger/dagger.gen.go`

via `Directory.file(path).contents` (or similar) against the context
directory. If either lookup errors with "not found", return a specific
error naming the missing path and suggesting `dagger develop`.

### What is NOT done in this path

- No `codegen generate-module` exec.
- No introspection JSON file creation / mount.
- No `withoutFile(dagger.gen.go)` stripping.
- No SSH / git / env plumbing that codegen needs (it's for the codegen
  binary's `go mod tidy` step, which is now irrelevant).
- No `go mod tidy` post-commands.

Everything else — `sdk.base()` (Go container + mod caches + GOPROXY), the
`go build` step in `Runtime()`, entrypoint wiring — stays identical.

## `dagger init` behavior

In `cmd/dagger/module.go`'s init flow, when writing a fresh `dagger.json`:

- When `--sdk=go` and no existing config exists (`IsInit=true` path):
  write both flags:

  ```json
  "codegen": {
    "automaticGitignore": false,
    "legacyCodegenAtRuntime": false
  }
  ```

- Any other SDK: do not write these keys; preserve existing defaults.
- `dagger develop` on an existing module: never auto-writes these flags.
  Users migrate existing modules manually (edit `dagger.json`, run
  `dagger develop` once to seed generated files, commit, done).

The concrete insertion point is where `dagger init` constructs
`modules.ModuleConfig.Codegen` before writing `dagger.json`.

## Missing-files error

When `Runtime()` takes the new path and the context directory lacks the
required generated files:

```
module "my-module" has codegen.legacyCodegenAtRuntime=false but generated
files are missing: <srcSubpath>/dagger.gen.go (or internal/dagger/…).
Run `dagger develop` to regenerate.
```

One short sentence, names the missing path, names the one command that
fixes it.

## Testing

### Unit tests

- `core/modules/config_test.go` (new or extended): `ModuleCodegenConfig.Validate`
  cases:
  - `nil` config → ok
  - `legacyCodegenAtRuntime=false, automaticGitignore=false` → ok
  - `legacyCodegenAtRuntime=false, automaticGitignore=nil` → error
  - `legacyCodegenAtRuntime=false, automaticGitignore=true` → error
  - `legacyCodegenAtRuntime=true, automaticGitignore=true` → ok (legacy)

### Integration tests (`core/integration`)

- `TestGoSDKCodegenAtRuntimeOptIn`: init a Go module (post-PR-2 `dagger init`
  writes the flags), add a trivial function, run `dagger develop` to
  seed generated files, observe that a subsequent `dagger call`
  succeeds and that its span trace does NOT contain
  `codegen generate-module` activity for the module.
- `TestGoSDKCodegenConfigValidation`: manually write `dagger.json`
  with `legacyCodegenAtRuntime=false` but `automaticGitignore=true` →
  expect module-load error.
- `TestGoSDKMissingGeneratedFiles`: flag set to false, delete
  `dagger.gen.go` from the module source → `dagger call` returns the
  "run `dagger develop`" error.
- Existing `TestSelfCalls` and the rest of `TestModule` continue to
  pass unchanged (legacy default preserved for modules that don't opt
  in).

### E2E (manual, via `skills/engine-dev-testing/with-playground.sh`)

- `dagger init --sdk=go`, inspect generated `dagger.json` has both
  flags.
- Edit `main.go`, add a function, `dagger develop`, commit, `dagger
  call <fn>` — trace shows no codegen-run step.

## Rollout

Single PR stacked on PR 1. Commit breakdown inside the PR:

1. `core/modules: add LegacyCodegenAtRuntime config field + Validate`
2. `core/sdk/go_sdk: skip codegen at runtime when opted in`
3. `cmd/dagger: dagger init --sdk=go writes legacyCodegenAtRuntime=false`
4. `core/integration: tests for Go SDK codegen-at-runtime opt-in`

### Backwards compatibility

- Existing `dagger.json` files with no `codegen.legacyCodegenAtRuntime`
  field: unchanged behavior — codegen runs at runtime as before.
- Existing `dagger.json` with `codegen.automaticGitignore` only:
  unchanged behavior.
- Future `dagger develop` on old modules: no forced upgrade, no
  auto-write. Users opt in manually.
- Other SDKs: untouched, field ignored.

### Forward path

If this mode proves robust, later work can:

- Teach other SDKs to honor the flag.
- Flip the default (make `nil` mean "new behavior") in a future major
  version.
- Remove the codegen-at-runtime path entirely once all SDKs honor it.

Out of scope for this PR — the flag explicitly *preserves* the legacy
path behind the default.

## Risks & open items

| Risk | Likelihood | Mitigation |
|---|---|---|
| User commits `dagger.gen.go` but it drifts from engine schema (e.g. engine upgraded) | Medium | `dagger develop` is the migration tool; if the drift manifests as a build error, the error points at the fix. Freshness detection is a future enhancement. |
| `dagger init --sdk=go` starts producing `dagger.json` files that older engines can't parse | Low — `codegen` struct is already versioned and has `omitempty` | Old engines silently ignore unknown fields in `omitempty` JSON. Verify by loading a new-shape `dagger.json` against the engine at the last released version. |
| Mistake: user sets only one of the two flags | Handled | Validation error at load. |
| `go build` failure when committed code references a Dagger type that no longer exists in current SDK | Low | Same error today if you edit code without re-running `dagger develop`. User runs `dagger develop` to re-sync. |

Not a risk, worth noting:

- This change has no CLI/UX visibility beyond the `dagger.json` field
  and the new `dagger init` default. `dagger call` / `dagger functions`
  work the same for the user; they just happen faster in the new mode.
- Cache keys in buildkit become simpler (module source only, no schema
  JSON dependency) for opted-in modules — a real perf win on cold
  cache.
