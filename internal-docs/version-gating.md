# Schema Version Gating

Use this when adding, moving, or debugging public GraphQL API surface, especially
workspace APIs, codegen-visible types, schema introspection, or generated SDK
output.

## Mental Model

Dagger serves a schema view for each module based on that module's configured
`engineVersion`. A module pinned to an older engine version should see the API
surface that existed for that version, even when running on a newer engine.

`engine.APIViewVersion` maps a declared version to its view:

- Releases keep their own view (`v0.21.6`, `v1.0.0`). Build metadata is
  dropped.
- From `engine.PrereleaseViewsVersion` (`v1.0.0-beta.22`) on, a prerelease
  keeps its own view: `v1.0.0-beta.22`, `v1.0.0-beta.23`, `v1.0.0-rc.1`. View
  gates compare views with strict semver, so these order before `v1.0.0` and
  after each other, and a gate at `AfterVersion("v1.0.0-beta.23")` separates
  beta.22 callers from later ones.
- Every v1.0.0 prerelease before that (`v1.0.0-0`, `v1.0.0-beta.1` through
  `v1.0.0-beta.21`) shares the `v1.0.0-beta.21` view. These callers all got the
  `v1.0.0` view while views dropped prereleases, and `v1.0.0-beta.21` is past
  every gate set in that time (the newest is `v1.0.0-beta.17`), so their API is
  unchanged. No gate can tell them apart; when behavior must differ between
  them, compare the caller's exact declared engine version instead, as
  `callerPastChangesetCwdCutover` in `core/schema/workspace.go` does.
- Prereleases of other versions before the cut keep dropping the prerelease
  (`v0.21.0-rc1` gets `v0.21.0`).
- Dev builds drop the prerelease (any prerelease mentioning `dev`, e.g.
  `v1.0.0-dev-<commit>` gets `v1.0.0`), so they share their release's view and
  pass every prerelease gate of it. Dev builds from `main` declare the next
  release from `internal/version/VERSION`, e.g. `v1.0.0-beta.22`, and get that
  view.

`__schemaVersion` echoes the view, so it reports `v1.0.0-beta.21` for a caller
declaring `v1.0.0-beta.19` or `v1.0.0-0`. SDK codegens compare it with strict
semver (e.g. nullable objects from `v1.0.0-beta.10`); the shared view keeps
those checks unchanged for pre-cut callers.

Clients and modules get their view from the same mapping. A module's view is
that of the `engineVersion` it declares; an engine refuses to load a module
whose view is past its own (`engine.CheckMaxAPIViewCompatibility`), so a
v1.0.0-beta.22 engine refuses modules declaring `v1.0.0-beta.23` or `v1.0.0`.
Dev builds share their release's view, so they accept any module of their
version. In-repo fixtures must not declare bare `v1.0.0`: a dev engine built
from `main` is a prerelease, and refuses them.

A client's version is that of the dagger CLI that starts its session
(`dagger session --version` overrides it), or the engine's for a nested client,
not of the SDK release it was generated with: an SDK client locked to a beta.21
SDK and run under a beta.22 CLI gets the beta.22 view.

`engine.IdentifierCasingVersion` (`v1.0.0-beta.22`, defined as
`engine.PrereleaseViewsVersion`) is the cut for the identifier casing changes.
Gate on it like any version:
`View(AfterVersion(engine.IdentifierCasingVersion))`,
`View(BeforeVersion(engine.IdentifierCasingVersion))`,
`core.Supports(ctx, engine.IdentifierCasingVersion)`, and
`engine.APIVersionAtLeast(declared, engine.IdentifierCasingVersion)` for a
declared version that is not carried by a call, such as a module's
`engineVersion`.

### If the cut slips a release

`engine.PrereleaseViewsVersion` is frozen once a release ships with it. If the
release it names ships without it (as `v1.0.0-beta.21` did), move the cut to
the next release:

1. In `engine/version.go`, set `PrereleaseViewsVersion` to the next release
   (`internal/version/VERSION` on `main`) and `basePrereleaseView` to the
   release that just shipped. `IdentifierCasingVersion` follows
   `PrereleaseViewsVersion`. `TestPrereleaseViewConstants` checks that
   `basePrereleaseView` is the beta just before the cut; the view tests in
   `engine` and `core/schema` derive their versions from the constants.
2. Audit gates set since the last bump:
   `git grep -nE '(After|Before)Version\("v1\.0\.0-|Supports\(ctx, "v1\.0\.0-'`,
   plus version constants passed to `core.Supports` (`git grep -n
   'Version = "v1\.0\.0-'`). A gate at or before the release that shipped
   still reaches every pre-cut caller. A gate at the new cut hides API from
   pre-cut callers who saw it before: decide on it explicitly.
3. Grep for the old cut (`git grep -n 'beta\.<old cut>'`) and move the
   mentions that mean "the cut" or "the last pre-cut release", in this file
   and in tests. Fixtures pinned to an earlier beta, such as
   `v1.0.0-beta.20`, stay pre-cut and need no change; fixtures declaring the
   old cut to test API past it must move to the new one; fixtures declaring
   bare `v1.0.0` must be pinned to a prerelease.
4. Downstream PRs and branches that build on the cut (the identifier casing
   changes) need the same bump wherever their gates, fixtures or docs declare
   the cut version.

The view is only part of a call's identity, and so of its cache key, when the
field has a view filter (any `.View(...)`, including `AllVersion`). A resolver
whose result depends on the caller's version (through `core.Supports`,
`core.CallerView`, or the server's view) must be view-scoped, or callers on
either side of a gate share one cached result. Each view from the cut on is a
separate cache key for view-scoped calls; callers before it share one.

The view is carried by dagql calls and IDs, and codegen consumes introspection
for that view. That means a new public API leaking into an old view can change
generated SDK/runtime files for old fixtures and modules.

Important entry points:

- `core.AfterVersion`, `core.BeforeVersion`, and `core.AllVersion` in
  `core/util.go`
- schema aliases in `core/schema/util.go`
- field and argument `.View(...)` filters in `core/schema/*.go`
- type-level filters such as `dagql.NewClass[*T](srv).View(...)`
- scalar, enum, input object, and typedef install filters in `dagql`
- `dagql.Server.SchemaForView(...)`
- `core/schema/query.go`, where introspection JSON includes `__schemaVersion`

## Key Commits

`c54a8fd00` (`fix(engine): ensure API version gates (#13418)`) is the main
reference commit. It fixed leaks where newer API surface appeared in older
views. The issue was not just stale field gates; dagql view filtering also
needed to apply beyond object classes, including enums, input objects,
interfaces, scalars, and legacy ID fields.

`b1fab4bec` (`test: opt contextual workspace modules into v1 API (#13456)`) is
the follow-up lesson from the Discord discussion on 2026-06-15. Contextual
workspace tests used `Workspace.cwd`, which is only visible in the v1.0 view
after the version-gating fix. Test modules pinned to `v0.21.5` failed during
module initialization, so those fixtures were bumped to `v1.0.0`.

## Adding Public APIs

When adding a public field, argument, type, enum value, scalar, input object, or
interface:

1. Decide the first module engine version that should see it.
2. Gate new future API with `View(AfterVersion("<version>"))`, the next
   release from `internal/version/VERSION` (e.g. `v1.0.0-beta.22`). From
   `engine.PrereleaseViewsVersion` on, a prerelease boundary separates the
   callers before and after it. Do not gate at a v1.0.0 prerelease before it:
   those callers share one view. Do not gate at bare `v1.0.0` unless the API
   is for the release only: under strict semver every beta is older.
   In tests, build views from declared versions with `engine.APIViewVersion`,
   not raw prerelease strings, so they match what callers actually get.
3. If replacing old shape with new shape, gate the legacy shape with
   `View(BeforeVersion("<cutover>"))` and the new shape with
   `View(AfterVersion("<cutover>"))`.
4. Gate every schema definition the API exposes, not only the field.
5. Run or inspect `TestBaseSchemaAllowlist` when changing public API surface.

`core/schema/schema_test.go` keeps `core/schema/base_schema.json` as the public
API allowlist for the oldest supported module view. If a new public API appears
there unexpectedly, it needs a version gate or an intentional allowlist update.

## Gate The Full Type Surface

Do not gate only the resolver field if the field exposes new schema types.
Check all of these surfaces:

- object classes: `dagql.NewClass[*T](srv).View(...)`
- object fields and args: `dagql.Func(...).View(...)` and
  `dagql.Arg(...).View(...)`
- enum values and whole enum installs
- input object installs and input fields
- interfaces and interface fields
- scalars
- typedef builders and legacy ID fields

The `#13418` bug happened because some filtering covered object classes but not
the complete schema definition set.

## Workspace V1 Tests

Most native workspace APIs are v1-gated in `core/schema/workspace.go` with
`AfterVersion("v1.0.0-0")`. That includes `Workspace.cwd`, `configFile`, native
workspace config mutations, module list/install/init APIs, and related
workspace object types.

When an integration fixture or synthetic module exercises new workspace APIs,
give that module a v1 engine view in `dagger.json`:

```json
{
  "name": "example",
  "engineVersion": "v1.0.0-beta.20",
  "sdk": {"source": "dang"}
}
```

A module declaring a v1.0.0 prerelease before `v1.0.0-beta.22`, such as
`v1.0.0-beta.12` or `v1.0.0-0`, gets the shared `v1.0.0-beta.21` view, which
has the whole v1 API gated before the cut. A fixture that exercises API gated
at the cut or later must declare a prerelease at or past it, such as
`v1.0.0-beta.22`. Do not declare bare `v1.0.0`: prerelease engines refuse it.
Do not leave a fixture pinned to `v0.21.x` if it
uses v1-only workspace fields; it may fail before the test reaches its
assertions because SDK/runtime generation uses the older schema view.

## Codegen Interaction

Codegen is schema-view sensitive:

- `SchemaIntrospectionJSONFileForModule` and client introspection use the active
  view.
- `cmd/codegen` reads `__schemaVersion` from introspection JSON.
- Go and TypeScript templates use `CheckVersionCompatibility` for generated
  surface compatibility.
- `dagger generate` can rewrite SDK/runtime files for every fixture whose
  configured view sees a schema change.

When a gate changes, check generated output for modules pinned to old views and
for modules intentionally using the new view. A correct gate should prevent old
views from changing unless the old view intentionally gained or lost API.

## Debugging Symptoms

Suspect version gating when:

- a module pinned to an old `engineVersion` sees a new field or generated
  runtime output changes unexpectedly
- a fixture using workspace APIs fails during module initialization after a
  schema-gating change
- `TestBaseSchemaAllowlist` shows new API in the base schema view
- generated SDK output changes across many languages after adding a field,
  enum, input, interface, or scalar

Start with the module's `dagger.json` `engineVersion`, then compare the field
and type filters in `core/schema/*.go` against the schema view the failing code
is actually using.
