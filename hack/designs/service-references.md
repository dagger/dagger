# Service References in Workspace Config

## Status: In Progress (`feat/named_ups`)

## Problem

Generic reusable modules cannot compose with each other when one module needs a
running service provided by another. For example:

- A **docusaurus** module knows how to start a documentation site (`+up` function).
- A **playwright** module knows how to run browser tests against a web app (`+check`
  function that accepts a `Service` constructor arg).
- Both modules are installed in the same workspace, but neither is aware of the other.

Today, there is no way in workspace configuration to wire the docusaurus service into
the playwright module's constructor. The user must write a custom module to glue them
together, defeating the purpose of reusable modules.

## Solution

Extend the `settings.*` constructor customization mechanism in `dagger.toml` to
support **function references**: a value that resolves to the output of a function
on another workspace module. Two types are supported today:

- **`Service`** — referencing a `+up` function (the original motivation).
- **`Container`** — referencing any function that returns a `Container`.

Other core types (`Directory`, `File`, `Secret`) may follow; the resolution
mechanism is type-agnostic.

### Config Syntax

A function reference is an inline TOML table with a `from` key whose value is a
colon-separated path identifying a function on another workspace module:

```toml
[modules.docusaurus]
source = "github.com/example/docusaurus@v1.0"

[modules.base-images]
source = "github.com/example/base-images@v1.0"

[modules.playwright]
source = "github.com/example/playwright@v1.0"
settings.app = { from = "docusaurus:serve" }
settings.base = { from = "base-images:chromium" }
```

The path format is `<module>:<function>` for singleton functions, or
`<module>:<function>:<collection-key>` for functions exposed by a collection member.

### Constraints

- For `Service` args, `from` resolves `+up` functions. For `Container` args, it
  resolves any zero-arg function returning a `Container`. It is not (yet) a
  general-purpose cross-module reference for arbitrary types.
- The target constructor argument must accept `*dagger.Service` or
  `*dagger.Container` (typically optional).
- Referenced functions take no arguments; a provider is parameterized via its
  own `settings.*` block, not at the reference site.
- The colon separator is consistent with existing `ModTreeNode.PathString()` convention.
- Invalid references (nonexistent module, nonexistent function, type mismatch)
  fail at runtime, not at config parse time.

### Module Author Side

A module that wants to accept a service from the workspace declares an optional
`Service` constructor argument:

```go
type Playwright struct {
    App *dagger.Service
}

func New(
    // +optional
    app *dagger.Service,
) *Playwright {
    return &Playwright{App: app}
}

// +check
func (p *Playwright) Test(ctx context.Context) error {
    _, err := dag.Container().
        From("mcr.microsoft.com/playwright:latest").
        WithServiceBinding("app", p.App).
        WithExec([]string{"npx", "playwright", "test"}).
        Sync(ctx)
    return err
}
```

The module has no knowledge of which workspace service will be wired in. It receives
a `*dagger.Service` like any other constructor argument.

### Engine Side

When the engine processes constructor arg defaults from `WorkspaceConfig`:

1. **Config parsing**: The `settings.*` value `{ from = "docusaurus:serve" }` is parsed
   as a `map[string]any` with a single `from` key. This is detected as a function
   reference (as opposed to a literal value).

2. **Resolution**: The colon-separated path is resolved to a function on the
   referenced workspace module. The engine evaluates the function and selects the
   ID of the object it returns (`Service`, `Container`). Resolution is type-agnostic:
   it builds dagql selectors from the path segments and grabs `id` off the result.

3. **Injection**: The resolved object is passed as the constructor argument default,
   the same way primitive values are injected today via `UserDefault()`.

4. **Load ordering**: Because module B's constructor depends on module A's
   output, module A must be loaded before module B. This creates an implicit dependency
   ordering between workspace modules.

### Collection Case

When a module uses Collections to dynamically expose multiple services:

```toml
# docusaurus detects 3 sites, exposes collection with keys "docs", "blog", "api"
settings.app = { from = "docusaurus:serve:docs" }
```

The third path segment identifies the collection member whose `+up` function provides
the service.

## Non-Goals

- **Service groups / profiles**: Running a named subset of services via `dagger up` is
  out of scope for this design. Will be addressed separately.
- **General-purpose cross-module references**: `from` is scoped to `Service` (via
  `+up`) and `Container`. Wiring other types (Directory, File, Secret) across
  modules is a natural follow-up but not in scope yet.
- **Config-time validation**: References are validated at runtime. Static config
  validation may be added later.

## Implementation Notes

### Key code paths

- **Config value detection**: `core/modfunc.go` — `UserDefault()` and
  `configValueToString()` currently handle only primitive types. Service references
  require detecting the `{ from = "..." }` map shape and resolving it differently.

- **Service resolution**: `core/up.go` / `core/modtree.go` — The `ModTreeNode` tree
  already supports discovering `+up` functions by path. Resolution evaluates the
  function via `DagqlValue()` to obtain a `Service`.

- **Config loading**: `engine/server/session_workspaces.go` — `ConfigDefaults` are
  marshaled as JSON and passed to `asModule`. The `from` map structure must survive
  this round-trip.

- **Module load ordering**: `engine/server/session_workspaces.go` — Modules are
  currently loaded with no guaranteed ordering between workspace modules. Service
  references introduce ordering constraints.
