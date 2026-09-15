# Schema Version Gating

Use this when adding, moving, or debugging public GraphQL API surface, especially
workspace APIs, codegen-visible types, schema introspection, or generated SDK
output.

## Mental Model

Dagger serves a schema view for each module based on that module's configured
`engineVersion`. A module pinned to an older engine version should see the API
surface that existed for that version, even when running on a newer engine.

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
2. Gate new future API with `View(AfterVersion("<version>"))`. For unreleased
   v1 API surface, the current pattern is `AfterVersion("v1.0.0-0")`.
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
  "engineVersion": "v1.0.0",
  "sdk": {"source": "dang"}
}
```

Use the matching `v1.0.0-*` prerelease view when the fixture is explicitly
testing a prerelease API view. Do not leave a fixture pinned to `v0.21.x` if it
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
