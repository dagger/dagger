# CLI Build Fails on Darwin: core/workspace imports engine-only code

## Status

Done

## What

`go build ./cmd/dagger` fails on macOS (darwin) with undefined symbol errors:

```
# github.com/dagger/dagger/engine/buildkit
engine/buildkit/executor_spec.go:1271:25: undefined: unix.OpenTree
engine/buildkit/executor_spec.go:1271:72: undefined: unix.OPEN_TREE_CLONE
engine/buildkit/executor_spec.go:1320:20: undefined: unix.Unshare
engine/buildkit/executor_spec.go:1320:33: undefined: unix.CLONE_FS
engine/buildkit/executor_spec.go:1324:20: undefined: unix.Setns
```

This builds fine on `main`. The regression was introduced on the `workspace` branch.

## Why

### Import chain

The workspace branch introduced `core/workspace`, a package used by the CLI for workspace detection and migration. Two files in this package import the `core` package:

```
cmd/dagger
  → core/workspace          (new on workspace branch)
    → core                  (engine-side package)
      → engine/buildkit     (Linux-only syscalls in executor_spec.go)
```

On `main`, `cmd/dagger` never imported `core` — it only communicated with the engine over a client connection. The workspace branch broke this boundary.

### Symbols used from `core`

`core/workspace/detect.go`:
- `core.StatFS` — interface (filesystem stat abstraction)
- `core.Host{}.FindUpAll()` — find-up directory traversal
- `core.StatFSExists()` — existence check wrapper

`core/workspace/migrate.go`:
- `core.FastModuleSourceKindCheck()` — string heuristic: "is this ref local?"
- `core.ModuleSourceKindLocal` — enum constant

`core/workspace/detect_test.go`:
- `core.FileType`, `core.FileTypeDirectory`, `core.FileTypeRegular`
- `core.Stat`, `core.StatFSFunc`

None of these usages need the heavy `core` package. `FindUpAll` is pure directory-walking logic. `FastModuleSourceKindCheck` is pure string heuristics. They happen to live in `core` alongside engine types that pull in `engine/buildkit`.

### Why not build tags?

Adding `//go:build linux` to `executor_spec.go` would fix the immediate error but would be a band-aid — the real problem is the layering violation. CLI code should not transitively compile engine internals.

## Fix

Remove the `core` import from `core/workspace` entirely with three changes:

### 1. `core/workspace/detect.go` — replace `core.StatFS` with a function parameter

Change `Detect` to accept an existence-check callback instead of `core.StatFS`:

```go
func Detect(
    ctx context.Context,
    pathExists func(ctx context.Context, path string) (parentDir string, exists bool, err error),
    readFile func(ctx context.Context, path string) ([]byte, error),
    cwd string,
) (*Workspace, error)
```

Inline the find-up loop (25 lines of `filepath.Dir` iteration). This removes the dependency on `core.Host{}`, `core.StatFS`, `core.StatFSExists`, and `core.FindUpAll`.

The caller in `engine/server/session.go` wraps its `core.StatFS`:

```go
ws, err := workspace.Detect(ctx, func(ctx context.Context, path string) (string, bool, error) {
    return core.StatFSExists(ctx, statFS, path)
}, readFile, cwd)
```

### 2. `core/workspace/migrate.go` — local `isLocalRef` helper

Replace 5 occurrences of `core.FastModuleSourceKindCheck(s, p) == core.ModuleSourceKindLocal` with a local function:

```go
func isLocalRef(source, pin string) bool {
    if pin != "" { return false }
    if len(source) > 0 && (source[0] == '/' || source[0] == '.') { return true }
    return !strings.Contains(source, ".")
}
```

This is 10 lines of stable string logic at a clean layer boundary. The original `FastModuleSourceKindCheck` stays in `core` for engine use.

### 3. `core/workspace/detect_test.go` — local test types

Replace `core.FileType`, `core.Stat`, `core.StatFSFunc` with local test helpers matching the new `pathExists` signature.

### 4. `engine/server/session.go` — adapt caller

Wrap the `core.StatFS` into the new `pathExists` callback when calling `workspace.Detect`.

### What stays the same

- `core/host.go` keeps `FindUpAll` — engine code still uses it
- `core/modulerefs.go` keeps `FastModuleSourceKindCheck` — engine code still uses it
- No new packages needed
