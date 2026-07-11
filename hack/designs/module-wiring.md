# Module Wiring

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

Extend the address mechanism that already backs `settings.*` values and CLI flags to
support **module wiring**: a value that resolves to the output of a function on
another installed workspace module. Two types are supported today:

- **`Service`** — conventionally referencing a `+up` function (the original
  motivation), though any `Service`-returning function resolves.
- **`Container`** — referencing any function that returns a `Container`.

Other core types (`Directory`, `File`, `Secret`) may follow; the resolution
mechanism is type-agnostic.

### Config Syntax

A module reference is a plain string of the form `<module>:<function>` — the same
address string that a `Container` or `Service` setting already accepts, with a new
interpretation when the first segment names an installed module:

```toml
[modules.docusaurus]
source = "github.com/example/docusaurus@v1.0"

[modules.base-images]
source = "github.com/example/base-images@v1.0"

[modules.playwright]
source = "github.com/example/playwright@v1.0"

[modules.playwright.settings]
app = "docusaurus:serve"
base = "base-images:chromium"
```

There is no wrapper table and no `from` key: the value is the address string
directly. `<module>` is the workspace **install name** (the `[modules.X]` key in the
same `dagger.toml`), and `<function>` is a zero-arg function on it.

### Semantics

A module reference is a **call-style value reference**: it injects the value that
evaluating the named function returns, not a lifecycle verb — nothing is "run". The
leading segment keys on the workspace **install name** (the `[modules.X]` key in the
same `dagger.toml`), not the module's type name; these usually coincide but diverge
when a module is installed under an alias, and the install name wins. The second
segment is a zero-arg function on that module. Any correctly-typed zero-arg function
resolves — `+up` is a *discovery convention* for services (its path is also a valid
call path, so `dagger up -l` is a convenient place to copy a `Service` ref from), not
a gate; `Container` refs have no verb at all.

#### Precedence: commit-on-match

Detection is decided entirely by the first segment, and the decision is final:

- If the first segment names a module **installed in this workspace**, the string is
  a module reference — period. From that point on, any failure (unknown function,
  return type that doesn't match the target argument, reference cycle) is a **hard
  error**. It never silently falls back to image or URL interpretation.
- If no install name matches, the string keeps its existing address meaning: an OCI
  ref for a `Container`, a `tcp://`/`udp://` URL for a `Service`.

This commit-on-match rule is what makes the behavior predictable: a typo in a
function name against a real module surfaces as an error naming the module and
function, rather than being reinterpreted as (say) an image pull of a nonexistent
registry path.

#### Shadowed images

Because the module name is matched first, installing a module whose name collides
with an image name shadows that image. For `Container` addresses, a fully-qualified
registry path never matches a module name and so keeps its image meaning:

```toml
[modules.playwright.settings]
base = "docker.io/library/chromium"   # the image, even if a module is named chromium
```

A dedicated scheme to force image interpretation (e.g. `oci://`) may be added later
if the fully-qualified form proves too clumsy; it is deliberately out of scope here.

There is no analogous collision for services: a module ref has no scheme, and
service addresses are `tcp://`/`udp://` URLs, so the `://` alone disambiguates.

#### Constraints

- Exactly two segments — `<module>:<function>` — are supported today. A matching
  module prefix followed by extra colons (e.g. `docusaurus:sites:serve`) is rejected
  with a clear error, not silently treated as an image ref. Deeper traversal is a
  forward-compatibility concern, addressed below.
- Supported arg types are scoped to `Service` and `Container` by documentation and
  test coverage, not by an engine-side type check. It is not (yet) a general-purpose
  cross-module reference for arbitrary types.
- The target constructor argument must accept `*dagger.Service` or `*dagger.Container`
  (typically optional).
- Referenced functions take no arguments; a provider is parameterized via its own
  `settings.*` block, not at the reference site.
- Invalid references (nonexistent function on a matched module, type mismatch, cycle)
  fail at runtime, not at config parse time.

#### Works everywhere addresses do

Because this lives in the address layer rather than in workspace-config parsing, a
module reference is usable anywhere an address is:

```shell
# workspace settings (dagger.toml)
app = "docusaurus:serve"

# CLI object flag
dagger call playwright --app=docusaurus:serve test

# dagger settings command
dagger settings playwright app docusaurus:serve
```

The last of these is new capability: the CLI `settings` command can express strings,
which it never could for the old table form.

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

## Implementation Notes

### Where it lives: the Address layer

Resolution is implemented in the address decoders in `core/schema/address.go`, not in
workspace-config parsing. A `settings.*` string (or a CLI object flag) is already
lowered to an `Address`, whose `.container()` / `.service()` accessors turn the string
into the underlying core object. Module-ref detection is a bare-string check inside
those decoders, so every address site — settings, flags, `dagger settings` — inherits
it for free.

The mechanics:

- **Bare-string detection.** A candidate is a string containing exactly one `:` with
  non-empty parts on both sides. Anything containing `://` (URL-ish: `tcp://`, …) is
  never a module ref.
- **Commit-on-match by Root field existence.** The first segment is normalized to a
  gql field name and looked up on the (non-canonical) current `Query` root's object
  type. Only if a field of that name **exists** — i.e. the module is installed — is
  the string committed as a module ref. This uses `FieldSpec` on the root object
  type, not a probing `Select`, so a non-match is a clean "not a module ref"
  rather than an error. The match is then confirmed by **module provenance**: core
  `Query` fields (`host`, `git`, `secret`, `container`, `http`, `module`, …) share the
  root namespace with module entrypoints, but only entrypoints carry a
  `FieldSpec.Module` (the module that provides the field). A field with no `Module` is
  a core field, so it is excluded here and retains its ordinary address meaning. Core
  field names are therefore effectively reserved words that module refs cannot use —
  `git:2.40` stays an image ref, never a module ref. (Checking `FieldSpec.Module`
  rather than a second canonical-root lookup is necessary because on the settings /
  object-flag decode path the current server *is* its own canonical, so both roots
  carry the module entrypoints.)
- **Typed Select for mismatch errors.** Once committed, resolution runs a two-step
  dagql `Select` from the `Query` root — module field, then function field — into the
  **typed** destination (`*dagql.ObjectResult[*core.Service]` /
  `[*core.Container]`). dagql's own typed Select produces the type-mismatch error when
  the function's return type doesn't match, so the error message is dagql-native.
- **Context-chain cycle guard.** The chain of in-flight module-ref strings is
  carried on the `context.Context`. Context values propagate through dagql `Select`
  into nested module construction, so re-entry of an in-flight ref is detectable, and
  a cycle fails fast with a clean `a -> b -> a` error rather than hanging the engine
  with unbounded goroutine growth.
Only `.container()` and `.service()` decode module refs today; the same hook can be
added to `.directory()`, `.file()`, `.secret()` when those target types are in scope.

### Lint: shadowing warning

Commit-on-match means installing a new module can change the meaning of an existing
string: a value that previously resolved as an image or URL becomes a module ref
the moment a module of that name is installed. A lint should warn when a newly
installed module name shadows a string that was previously address-resolved (image or
URL), so the change in interpretation is surfaced rather than silent. A
fully-qualified registry path is the documented remedy.

### Key code paths

- **Module-ref detection & resolution**: `core/schema/address.go` —
  `resolveModuleRef` performs bare-string detection, commit-on-match via
  `root.ObjectType().FieldSpec(...)`, the two-segment check, the context-chain cycle
  guard, and the typed two-step `srv.Select(ctx, root, dest, ...)`. It is called from
  `(*addressSchema).container` and `(*addressSchema).service`.

- **ModTree-free, dagql-Select approach**: resolution runs selectors straight from the
  dagql server `Root()` and relies on dagql's typed `Select` for both traversal and
  type enforcement. This is intentionally **ModTree-free**: it does not use
  `core/up.go`, `core/modtree.go`, or `ModTreeNode.PathString()`. The design is
  forward-compatible with modules-v2, which drops the ModTree/CheckGroup path — do not
  "fix" this toward ModTree.

- **Config loading**: `engine/server/session_workspaces.go` — `ConfigDefaults` are
  marshaled as JSON and passed to `asModule`. Because the reference is now a plain
  string, no special map structure has to survive this round-trip.

- **Module load ordering**: `engine/server/session_workspaces.go` — because module B's
  constructor depends on module A's output, A must resolve before B. This creates an
  implicit dependency ordering between workspace modules; the cycle guard above
  catches the pathological case.

## Non-Goals

- **Service groups / profiles**: Running a named subset of services via `dagger up` is
  out of scope for this design. Will be addressed separately.
- **General-purpose cross-module wiring**: today's references are scoped to
  `Service` (via `+up`) and `Container`. Wiring other types (Directory, File, Secret)
  across modules is a natural follow-up but not in scope yet.
- **Config-time validation**: References are validated at runtime. Static config
  validation may be added later.
- **Deep / collection traversal**: only two segments are accepted. Deeper paths and
  collection-member selection are deferred to the value-coordinates design below,
  which claims that space deliberately.

## Forward compatibility: value coordinates

The two-segment string is a deliberately small foothold. It is chosen to sit cleanly
under the agreed long-term design — **value coordinates** from modules-v2 — so that
nothing accepted today has to change meaning tomorrow. This section frames the
short-term choice by the long-term one. It cross-references the `type:field` bridge in
`hack/designs/modules-v2/artifacts.md`; the description here is written to be
standalone-accurate.

### Coordinates

Under modules-v2 artifacts/collections, every selectable value carries a **coordinate**
— a row of cells over a set of named **dimensions**:

- Dimension names are **kebab-cased schema type names**. The engine already
  module-namespaces object types (`namespaceObject`), so a module's types are distinct
  across the workspace before kebab-casing; the dimension name is derived purely
  syntactically from that namespaced type name.
- Alongside those, there are **core-type dimensions** — `service`, `container`, and so
  on — one per selectable core type.
- A coordinate is the tuple of cells over the applicable dimensions; a `null` cell
  means the dimension does not apply to that value.

### References are coordinate cells

A reference is a way of spelling a coordinate cell:

- The **common case** is a single bare key string — exactly the `<module>:<function>`
  string used today.
- A **multi-coordinate** reference is a TOML table, `{ dimension = "value", … }`, one
  entry per pinned dimension.

This is precisely why the present design leaves *two* syntactic spaces unclaimed:

- The **single-segment string space** — a bare key with no `:` — stays free for a
  future single-coordinate cell.
- The **`{ }` table space** — an inline TOML table — stays free for the future
  multi-coordinate table form. (This is also why the earlier `{ from = … }` wrapper
  and the `sites[docs]` bracket-collection syntax were dropped: both squatted on space
  the coordinate design needs.)

### Today's `"foo:bar"` reinterpreted forward

Today's `"<module>:<function>"` string is read forward as the `<type>:<field>`
selector defined by the artifacts `type:field` bridge, with the first segment a `type`
coordinate and the remainder an artifact-relative field path. This reinterpretation is
**safe by construction**:

- The engine collapses a module's main-object type name to the module's final install
  name (`namespaceObject` plus kebab-casing yields the same token the install name
  produces).
- Therefore `<install>:<function>` (today's reading) and `<type>:<field>` (the forward
  reading) designate the **same node** for every string accepted today — aliases
  included, because the install name is exactly what drives both.

No string that resolves today resolves to a different node under the forward reading;
the change is in vocabulary, not in destination.

### Arity from the sink

A coordinate selector is always a **constraint set**, not a commitment to singular or
plural. Whether a given selection yields one value or many is resolved by the
**sink** — the receiving argument's type supplies the expected arity and nominates the
primary dimension:

- A `*dagger.Service` argument expects one value; the selector is required to pin down
  to a single coordinate, and the primary dimension is `service`.
- A collection-typed argument expects many; the same selector vocabulary yields the
  set.

"Arity from the sink" means the string never encodes singular-vs-plural itself. That
is deliberately the job of the receiving type, which keeps the string uniform across
call sites.

### The mantra

- **Keys go in cells.** A collection member's key is a coordinate value, carried in a
  cell (a bare string in the common case, a table entry when multiple dimensions are
  pinned) — never spelled inside the selector string.
- **The qualifier is `type:field`.** The only structure inside a reference string is
  the `type:field` selector.
- **Collection traversal never enters strings.** Reaching a specific member is done by
  pinning a coordinate cell, not by growing the string with `[key]` or extra colons.
  This is why deep/bracketed paths are rejected today: that syntactic space belongs to
  cells, not to strings.
