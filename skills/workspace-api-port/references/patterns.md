# Workspace API — Transformation Patterns for Toolchain Modules

## Background

The Workspace API (dagger/dagger#11812) deprecates `+defaultPath`/`+ignore` annotations and the
separate "toolchain" concept. Instead of the engine magically injecting host directories into
function arguments via static pragmas, modules explicitly receive a `*dagger.Workspace` and call
`ws.Directory(path)` or `ws.File(path)` to dynamically access project files. This makes every
module a potential toolchain — no special designation needed.

Key constraint: `Workspace` **cannot be stored as a struct field** — it must be a function argument.
But `*dagger.Directory` / `*dagger.File` derived from it **can** be stored.

## Workspace SDK API (Go)

```go
// Auto-injected by the engine when declared as a function argument
ws *dagger.Workspace

// Get a directory from the workspace (lazy — no ctx needed)
ws.Directory(path string, opts ...dagger.WorkspaceDirectoryOpts) *dagger.Directory

// Get a file from the workspace (lazy — no ctx needed)
ws.File(path string) *dagger.File

// Opts for Directory
type WorkspaceDirectoryOpts struct {
    Include []string
    Exclude []string
}
```

## Pattern A: Subdirectory with fixed default path (helm-dev)

Use when `+defaultPath` points to a specific subdirectory.

**Before:**
```go
func New(
    // +optional
    // +defaultPath="/helm/dagger"
    chart *dagger.Directory,
) *HelmDev {
    return &HelmDev{Chart: chart}
}
```

**After:**
```go
func New(
    ws *dagger.Workspace,
    // Path to the helm chart directory in the workspace
    // +optional
    // +default="helm/dagger"
    chartPath string,
) *HelmDev {
    return &HelmDev{Chart: ws.Directory(chartPath)}
}
```

Notes:
- The `*dagger.Directory` arg is replaced by `ws *dagger.Workspace` + a `string` path arg
- The path arg gets `+default` with the old `+defaultPath` value (strip leading `/`)
- The path arg is configurable via workspace config: `config.chartPath = "other/path"`
- `ws.Directory()` is lazy (no ctx needed), so the constructor signature stays simple

## Pattern B: Root directory with include/exclude (go toolchain)

Use when `+defaultPath="/"` (whole project root) and `+ignore` patterns are involved.

**Before (main.go):**
```go
func New(
    // +defaultPath="/"
    source *dagger.Directory,
    // ... other args
) *Go {
    if source == nil {
        source = dag.Directory()
    }
    // ...
}
```

**Before (dagger.json customizations):**
```json
{
    "name": "go",
    "source": "toolchains/go",
    "customizations": [{
        "argument": "source",
        "ignore": ["bin", ".git", "**/node_modules", "..."]
    }]
}
```

**After (main.go):**
```go
func New(
    ws *dagger.Workspace,
    // Include only files matching these patterns
    // +optional
    include []string,
    // Exclude files matching these patterns
    // +optional
    exclude []string,
    // ... other args
) *Go {
    source := ws.Directory(".", dagger.WorkspaceDirectoryOpts{
        Include: include,
        Exclude: exclude,
    })
    // ...
}
```

**After (dagger.json):** Remove `customizations` block entirely.
```json
{
    "name": "go",
    "source": "toolchains/go"
}
```

Notes:
- `+defaultPath="/"` becomes `ws.Directory(".")`
- The `source == nil` fallback is no longer needed (workspace is always injected)
- `+ignore` patterns from dagger.json customizations become `exclude` constructor args
- Those patterns migrate to workspace config: `config.exclude = ["bin", ".git", ...]`
- If the struct already has `Include`/`Exclude` fields, populate them from the new args

## Pattern C: File with fixed path (docs-dev nginx.conf)

Use when `+defaultPath` points to a specific file.

**Before:**
```go
func New(
    // +defaultPath="/docs/nginx.conf"
    nginxConf *dagger.File,
) *DocsDev {
    return &DocsDev{NginxConf: nginxConf}
}
```

**After:**
```go
func New(
    ws *dagger.Workspace,
    // Path to the nginx config file
    // +optional
    // +default="docs/nginx.conf"
    nginxConfPath string,
) *DocsDev {
    return &DocsDev{NginxConf: ws.File(nginxConfPath)}
}
```

## Pattern D: Multiple +defaultPath args in one constructor

When the constructor has multiple `+defaultPath` args (e.g. a directory AND a file), add
a path arg for each and derive both from the single `ws` argument.

## Pattern E: +defaultPath on a non-constructor function

When `+defaultPath` is on a regular method (not constructor), add `ws *dagger.Workspace` as a
parameter to that function and derive the directory/file inside the function body.
Do NOT store workspace on the struct — that violates the constraint.

**Before:**
```go
func (m *MyMod) Deploy(
    ctx context.Context,
    // +defaultPath="/"
    source *dagger.Directory,
) error {
    // use source
}
```

**After:**
```go
func (m *MyMod) Deploy(
    ctx context.Context,
    ws *dagger.Workspace,
) error {
    source := ws.Directory(".")
    // use source
}
```

## dagger.json cleanup

After porting a module, check the root `dagger.json` for `customizations` entries that reference
the removed argument. Remove the entire `customizations` block if all its entries referenced
the old arg. The patterns move to workspace config (`config.*` entries).
