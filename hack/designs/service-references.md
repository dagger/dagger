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

- **`Service`** — conventionally referencing a `+up` function (the original
  motivation), though any `Service`-returning function resolves.
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

The path format is `<module>:<function>` for singleton functions. Each
colon-separated segment is a navigation step (a zero-arg function/field). When
collections land (see [#13299](https://github.com/dagger/dagger/pull/13299)), a
collection member is selected by appending `[<key>]` to the collection segment —
e.g. `<module>:<collection>[<key>]:<function>`. Brackets distinguish a keyed
member selection (`get(<key>)`) from a plain navigation step, so the path scales
to arbitrary nesting and multiple collections (see [Collection Case](#collection-case)).

### Constraints

- `from` resolves any zero-arg function on a workspace module whose return type
  matches the target arg. `+up` is not a gate: it governs `dagger up` lifecycle
  and listing (`dagger up -l` is where users naturally copy ref strings from),
  not referenceability. For `Service` args the conventional target is a `+up`
  function, but any `Service`-returning function works.
- Supported arg types are scoped to `Service` and `Container` by documentation
  and test coverage, not by an engine-side type check. It is not (yet) a
  general-purpose cross-module reference for arbitrary types.
- The target constructor argument must accept `*dagger.Service` or
  `*dagger.Container` (typically optional).
- Referenced functions take no arguments; a provider is parameterized via its
  own `settings.*` block, not at the reference site.
- The colon separator is consistent with existing `ModTreeNode.PathString()`
  convention. The `[<key>]` member-selection syntax (collections only) extends
  that convention; `dagger up -l` is expected to print bracketed keys so refs are
  always copied, not hand-constructed.
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
   A bare segment becomes a zero-arg field selector; once collections land, a
   `[<key>]` suffix becomes a `get(<key>)` selector on the preceding collection.
   (Today only the bare-segment path is implemented.)

3. **Injection**: The resolved object is passed as the constructor argument default,
   the same way primitive values are injected today via `UserDefault()`.

4. **Load ordering**: Because module B's constructor depends on module A's
   output, module A must be loaded before module B. This creates an implicit dependency
   ordering between workspace modules.

### Collection Case

> Collections are still in design ([#13299](https://github.com/dagger/dagger/pull/13299)).
> This section is forward-looking; nothing here is implemented yet, and the
> singleton `<module>:<function>` path above is unaffected.

When a module uses Collections to dynamically expose multiple members, a member
is selected by key with `[<key>]`:

```toml
# docusaurus detects 3 sites, exposing a `sites` collection with keys
# "docs", "blog", "api"; each member has a +up `serve` function.
settings.app = { from = "docusaurus:sites[docs]:serve" }
```

A bare segment is a navigation step (a zero-arg function/field); `[<key>]`
selects a collection member, resolving to `get(<key>)` on the preceding
collection per the collections algebra. This is why `[]` is needed rather than a
third bare segment: a member is reached via a keyed `get`, not a field named
after the key.

#### Why `[<key>]` instead of `<module>:<function>:<key>`

The earlier `:`-only form assumed a single, terminal collection dimension. A path
is really an arbitrary-depth chain that interleaves navigation steps with keyed
member selections, so the bracket form is needed to:

- **Nest arbitrarily** — collections within collections:

  ```toml
  settings.app = { from = "docusaurus:sites[docs]:regions[us-east]:serve" }
  ```

- **Disambiguate multiple collections of the same type** — collections are
  addressed by their field path, not their type, so two `Site` collections at
  different locations stay distinct:

  ```toml
  settings.a = { from = "infra:staging[web]:serve" }
  settings.b = { from = "infra:prod[web]:serve" }
  ```

- **Distinguish a key from a field** — `[<key>]` is unambiguous even when a
  collection key collides with a sibling field name.

For keys containing `:`, `[`, or `]`, a structured escape hatch may be added
later (e.g. `{ from = { module = "docusaurus", path = ["sites", { get = "docs" }, "serve"] } }`);
the common case stays the string form.

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
  function via `DagqlValue()` to obtain a `Service`. `resolveFunctionRef` in
  `core/modfunc.go` splits the path on `:` and builds one zero-arg selector per
  segment. Collection support (future) parses a `[<key>]` suffix into a
  `get(<key>)` selector; the bare-segment path is unchanged.

- **Config loading**: `engine/server/session_workspaces.go` — `ConfigDefaults` are
  marshaled as JSON and passed to `asModule`. The `from` map structure must survive
  this round-trip.

- **Module load ordering**: `engine/server/session_workspaces.go` — Modules are
  currently loaded with no guaranteed ordering between workspace modules. Service
  references introduce ordering constraints.
