# Conflict Resolution Notes

This records the merge conflict resolutions applied while merging `upstream/main` into `interfaces`.

## Overall resolution policy

For conflicts between upstream's dagql cache migration and the `interfaces` branch's interface/ID work, I preserved the `interfaces` branch's architectural direction:

- module interfaces are first-class GraphQL/dagql interfaces (`dagql.Interface`), not fake object classes;
- `InterfaceAnnotatedValue`, `wrapIface`, schema `asFoo` fields, and per-type/interface `loadFooFromID` loaders stay removed;
- object loading goes through global `node(id:)`;
- IDs use the unified `ID` scalar plus `@expectedType`, not per-type `FooID`/`DynamicID` scalars.

Where upstream changed the dagql cache/result model, I ported those changes forward:

- module/type metadata is carried as attached `dagql.ObjectResult[...]` values;
- `ID()` returns `(*call.ID, error)`;
- result identity is based on attached result IDs / `ResultCall` frames;
- schema resolution for loaded IDs goes through the engine cache and result-call provenance;
- new server forking and operation-lease paths from upstream were retained.

## Files resolved

### `core/interface.go`

Resolved the main conflict by keeping the first-class interface model and adapting it to upstream's dagql cache API.

Kept / restored from `interfaces`:

- `InterfaceType.Install` installs a `dagql.Interface`, not a `Class[*InterfaceAnnotatedValue]`.
- Interface field specs are schema declarations only; no per-interface resolver class is installed.
- Interface `id` field uses unified `dagql.AnyID{}`.
- Interface-specific `loadFooFromID` registration is not restored.
- `InterfaceAnnotatedValue`, `wrapIface`, result-call clone helpers, and wrapper-based covariance support remain deleted.
- `interfaceTypedMarker` remains the runtime `Typed` marker for interface return types.

Ported from upstream:

- `InterfaceType.mod` is `dagql.ObjectResult[*Module]`.
- `TypeDef(ctx)` returns `dagql.ObjectResult[*TypeDef]` using `SelectTypeDef`.
- `loadImpl` resolves the producing call from `dagql.EngineCache(...).ResultCallByResultID`, then uses `Query.ModDepsForCall` and `deps.Schema(ctx)` to load with the right module dependencies.
- `ConvertFromSDKResult` accepts already-attached `dagql.AnyObjectResult` values and validates subtype relationships via attached typedef results.
- `ConvertToSDKInput` handles any `dagql.IDable`, propagating `ID()` errors and nil IDs.
- `Install` uses upstream-style module provenance (`NewUserMod(iface.mod).ResultCallModule(ctx)`) and attached typedef/source-map access via `.Self()`.
- `@expectedType` propagation through list wrappers was kept and updated for attached `TypeDef` objects.

`CollectContent` was simplified to fit first-class interfaces: resolve the interface value's attached ID, load the concrete implementation through `loadImpl`, then delegate to the concrete type's `CollectContent`.

### `core/typedef.go`

Resolved interface/ID conflicts consistently with first-class interfaces and unified IDs.

- Removed upstream's `DynamicID` type.
- `TypeDef.ToTyped()` for interfaces returns `interfaceTypedMarker` instead of `InterfaceAnnotatedValue`.
- `TypeDef.ToInput()` for objects and interfaces returns `dagql.AnyID{}`.
- Function argument schema generation keeps `@expectedType` directives for object/interface ID arguments, including list-wrapped IDs, updated to use attached results (`.Self()`).
- Source-map directives use upstream's attached source-map access.

### `core/object.go`

Resolved conflicts by removing old interface-wrapper handling while keeping upstream's attached-result conversion model.

- Removed `InterfaceAnnotatedValue` conversion/collection paths.
- `ConvertToSDKInput` handles:
  - attached `dagql.ObjectResult[*ModuleObject]` values;
  - raw `*ModuleObject` values;
  - any `dagql.IDable`, loaded through the current dagql server.
- Conversion uses `moduleObjectFieldsToSDKInput` and parent `ResultCall` frames so field values are converted with correct child-call context.
- `CollectContent` now requires a `ModuleObject`, obtains the value's `ResultCall`, and uses `dagql.ChildFieldCall` when walking fields.

### `core/schema_build.go`

Resolved by combining upstream schema construction/forking with first-class interface registration.

Ported from upstream:

- use `coreSchemaForker` / `ForkSchema` when a core schema base is available;
- otherwise use upstream `dagql.NewServer(ctx, root)` initialization;
- install modules through `Mod`/`userMod` wrappers and attached typedef results.

Preserved from `interfaces`:

- object-interface conformance is registered through `dagql.InterfaceImplementor` and `ImplementInterfaceUnchecked`;
- no `asFoo` fields are generated;
- interface-to-interface implementation registration remains.

The `node(id:)` loader was updated to use upstream's result-call cache lookup (`ResultCallByResultID`) and `Query.ModDepsForCall`, replacing the old `Query.IDDeps` path.

### `dagql/server.go`

Resolved server conflicts by keeping upstream's new server/fork/cache structure while preserving global object identification and first-class interface support.

- Kept `SetNodeLoader` and `node(id:)`.
- Kept the `Node` auto-interface and auto-implementation behavior.
- Kept upstream `NewServer(ctx, root) (*Server, error)` and `Fork` structure.
- `newBlankServer` initializes `interfaces` and `autoInterfaces` state.
- `Fork` carries interfaces, auto-interfaces, canonical server, and node loader forward.
- Kept core directives from the `interfaces` branch, especially `@expectedType`.
- Dropped upstream's automatic per-object `loadFooFromID` registration in `InstallObject`.
- Kept operation lease handling in `resolvePath` and also kept `__typename` handling.
- Removed upstream's `InterfaceValue` fallback in `toSelectable` because interface wrapper values no longer exist.
- Kept the custom `possibleFragmentSpreadsRule` for interface-implements-interface validation.

### `dagql/objects.go`

Resolved by keeping upstream's cache-aware object result mechanics while preserving first-class interface implementation bookkeeping.

- `Class` uses `sync.RWMutex` from upstream.
- `Class` still has an `interfaces` map and implements `InterfaceImplementor`.
- The default `id` field returns unified `AnyID` via `NewResultForCurrentCall(ctx, NewAnyID(...))`.
- Kept upstream's internal `recipe` argument behavior for ID fields.
- Did not use upstream `NewDynamicID` / typed `FooID` behavior.
- `ForkObjectType` clones the `interfaces` map so forked schemas retain interface conformance.

### `dagql/types.go`

Resolved type conflicts in favor of unified IDs and removing interface wrapper support.

- Kept `PostCallable` from the branch.
- Dropped upstream `InterfaceValue` because `InterfaceAnnotatedValue` is removed.
- Updated `AnyID.ID()` to match upstream's `IDable` signature: `(*call.ID, error)`.
- Kept the unified `ID` scalar and `ExpectedTypeDirective` helper.

### `dagql/cache.go`

Resolved `ObjectResult` method conflicts in favor of upstream's new cache/session-resource paths.

- Kept `WithSessionResourceHandleAny`.
- Kept `objectResultWithDerefView` / `withDerefViewAny`.
- Dropped branch-local post-call/safe-to-persist object result methods that no longer match upstream's dagql cache model.

### `core/modfunc.go`

Resolved the return-value conflict by keeping the first-class-interface branch's `ensureSelectable` behavior and upstream's narrower workspace-content digest behavior.

- Return values are still upgraded to selectable object results when possible so cross-module object selections keep object type information.
- Workspace content digests are applied only when a function has workspace args and returns a module object type, matching upstream's dagql cache behavior.
- Removed old post-call/safe-to-persist cache handling from the conflicted block.
- Updated `ensureSelectable` to reconstruct dynamic arrays via `NewResultForCall`/`ResultCall`, not the removed `NewResultForID` path.

### `core/telemetry.go`

Resolved loader/meta conflicts for global object identification.

- Treat `node` as the loader/meta field.
- Do not restore `loadFooFromID` telemetry special cases.

### `core/schema/modulesource.go`

Resolved module-initialization result handling in favor of upstream attached object results.

- Module initialization expects `dagql.ObjectResult[*core.Module]`.
- Removed explicit `PostCall` handling from the old path.

### `core/sdk/module_typedefs.go` and `core/sdk/go_sdk.go`

Resolved module typedef loading conflicts in favor of upstream attached-ID loading.

- Decode/read the module ID emitted by the codegen container.
- Load it with `dagql.NewID[*core.Module](...).Load(ctx, dag)`.
- Do not route this through a GraphQL `node(id:)` query.

### `dagql/dagql_test.go`

Resolved test conflicts to match the new dagql cache/result-call behavior and global `node(id:)` behavior.

- Kept `slices` import; dropped unused `strconv`.
- `TestIDsReflectQuery` uses upstream recipe-ID/nth assertions for attached enumerable children.
- Kept the branch's `node(id:)`-based assertions where they still apply.
- Kept `TestImpureIDsReEvaluate`, adapted to the current test helpers (`newExternalDagqlServerForTest`, `newTestClient`).

### Removed files

These were delete/modify conflicts where upstream deleted the file, and keeping the file would reintroduce stale/duplicated paths:

- `core/sdk/dang_op.go`
- `core/typedef_from_schema.go`

## Validation performed

- Ran `gofmt` on all touched Go files.
- Ran `git diff --check` for the resolved Go files; no whitespace/conflict-marker issues in those files.
- Confirmed no unresolved conflict markers remain under `core` or `dagql` Go source files.
- Attempted `go test ./dagql -run TestLoadingFromID`, but it cannot run yet because root `go.mod` still contains merge conflict markers.

## Remaining conflicts at stop time

I stopped before resolving generated/module metadata conflicts. At the time of this summary, unresolved conflicts remain outside the Go source files resolved above, including:

- root `go.mod` / `go.sum`;
- generated SDK/API/reference files;
- generated introspection/schema docs;
- module/toolchain `go.mod` / `go.sum` files;
- `toolchains/test-split/dagger.json`.

Use this to inspect the exact remaining set:

```bash
git diff --name-only --diff-filter=U
```
