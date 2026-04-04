# Persist `moduleTypes` as Saved `Module` State

*Builds on [Stable Save/Load State for Dagger Objects](./save-load-object-state.md)*

## Status: Draft (`codegen-moduletypes`)

## Summary

This design stops treating `moduleTypes` as a live SDK codegen problem.

Instead:

1. the engine saves the initialized `Module` typedef state to a checked-in JSON
   artifact during `generatedContextDirectory`
2. module loading reuses that artifact on the next load
3. the SDK `moduleTypes` hook remains as a fallback for bootstrap and stale or
   missing artifacts

New artifact:

```text
<module-source-subpath>/dagger.moduletypes.json
```

That file contains:

- a version number
- the content-scoped module source digest it was generated from
- the stable saved `Module` state payload from
  [`__saveModuleState`](./save-load-object-state.md)

With that file present and current, `dagger functions` no longer needs to run
SDK typedef codegen at runtime.

## Problem

Today the engine has no portable representation of `moduleTypes`.

The current path is:

```text
module load
  -> SDK moduleTypes()
  -> SDK-specific live introspection / codegen
  -> JSON ModuleID
  -> loadModuleFromID
```

For Go, that live typedef generation still happens in
[`(*goSDK).ModuleTypes`](../../core/sdk/go_sdk.go), which runs
`codegen generate-typedefs` inside the SDK container.

That means:

- `dagger generate` does not persist the typedef result
- `dagger functions` still depends on live SDK work
- the engine cannot trust a checked-in artifact

The missing piece is not “another generated Go entrypoint”.
It is “a portable, engine-readable saved `Module` state”.

## Proposal

Persist the initialized `Module` state to a tracked JSON file and short-circuit
future `moduleTypes` loads through that file.

New shape:

```text
dagger generate / generatedContextDirectory
  -> load module normally once
  -> __saveModuleState(module.id)
  -> write dagger.moduletypes.json

later module load
  -> if dagger.moduletypes.json exists and digest matches:
       __loadModuleFromState(state)
     else:
       SDK moduleTypes()
```

This keeps the current SDK contract as fallback, but removes live SDK work from
the steady-state path.

## Artifact Format

New file:

```text
<module-source-subpath>/dagger.moduletypes.json
```

The file format is:

```go
type moduleTypesArtifactV1 struct {
	ArtifactVersion    int             `json:"artifactVersion"`
	ModuleSourceDigest string          `json:"moduleSourceDigest"`
	State              json.RawMessage `json:"state"`
}
```

Rules:

1. `ArtifactVersion` starts at `1`.
2. `ModuleSourceDigest` is the content-scoped digest already used during module
   load in [`initializeSDKModule`](../../core/schema/modulesource.go#L3068).
3. `State` is the exact JSON string returned by `__saveModuleState`.

Example:

```json
{
  "artifactVersion": 1,
  "moduleSourceDigest": "sha256:6f5b...",
  "state": {
    "type": "Module",
    "version": 1,
    "state": {
      "description": "Test module",
      "objects": [
        {
          "kind": "OBJECT_KIND",
          "optional": false,
          "asObject": {
            "name": "Test",
            "originalName": "Test"
          }
        }
      ]
    }
  }
}
```

## Generate-Time Write Path

Write the artifact in [`runGeneratedContext`](../../core/schema/modulesource.go#L2748).

That function already:

1. runs SDK codegen
2. generates clients
3. writes `dagger.json`
4. returns the generated context overlay

Extend it with one more step when the module has both a name and SDK:

1. load the module through normal `asModule`
2. call `__saveModuleState`
3. wrap it in `moduleTypesArtifactV1`
4. write `dagger.moduletypes.json` into the generated context directory

Exact insertion point:

after `runCodegen` / client generation and before the final return from
[`runGeneratedContext`](../../core/schema/modulesource.go#L2748).

Exact operations:

```go
scopedSourceDigest := srcInst.Self().ContentScopedDigest()

var mod dagql.ObjectResult[*core.Module]
err = dag.Select(ctx, srcInst, &mod, dagql.Selector{Field: "asModule"})

var stateJSON string
err = dag.Select(ctx, dag.Root(), &stateJSON,
	dagql.Selector{
		Field: "__saveModuleState",
		Args: []dagql.NamedInput{{
			Name:  "id",
			Value: dagql.NewID[*core.Module](mod.ID()),
		}},
	},
)

artifactBytes, err := json.MarshalIndent(moduleTypesArtifactV1{
	ArtifactVersion:    1,
	ModuleSourceDigest: scopedSourceDigest,
	State:              json.RawMessage(stateJSON),
}, "", "  ")
artifactBytes = append(artifactBytes, '\n')

artifactPath := filepath.Join(sourceSubpathOrRoot(srcInst.Self()), "dagger.moduletypes.json")
```

Then write the file with `withNewFile`.

`sourceSubpathOrRoot` means:

- `SourceSubpath` if non-empty
- otherwise `SourceRootSubpath`

The artifact belongs next to the module implementation, not next to the
workspace root.

## Load-Time Fast Path

Add the fast path at the start of
[`runModuleDefInSDK`](../../core/schema/modulesource.go#L2875), before calling
the SDK `moduleTypes` hook.

Current code:

```go
typeDefsImpl, typeDefsEnabled := src.Self().SDKImpl.AsModuleTypes()
if typeDefsEnabled {
	resultInst, err = typeDefsImpl.ModuleTypes(ctx, mod.Deps, srcInstContentHashed, mod.ResultID)
	...
}
```

Replace that with:

1. try to read `dagger.moduletypes.json` from the raw source context
2. if missing, keep the current path
3. if present, decode and validate it
4. if the digest matches `srcInstContentHashed.ID().Digest().String()`, load the
   saved state through `__loadModuleFromState`
5. only call SDK `moduleTypes` when the artifact is missing, malformed, or stale

Pseudo-code:

```go
if typeDefsEnabled {
	initialized, ok, err = s.tryLoadModuleStateArtifact(ctx, src, srcInstContentHashed)
	if err != nil {
		return nil, err
	}
	if !ok {
		resultInst, err := typeDefsImpl.ModuleTypes(ctx, mod.Deps, srcInstContentHashed, mod.ResultID)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize module: %w", err)
		}
		initialized = resultInst.Self()
	}
}
```

New helper in [`core/schema/modulesource.go`](../../core/schema/modulesource.go):

```go
func (s *moduleSourceSchema) tryLoadModuleStateArtifact(
	ctx context.Context,
	src dagql.ObjectResult[*core.ModuleSource],
	srcInstContentHashed dagql.ObjectResult[*core.ModuleSource],
) (_ *core.Module, ok bool, _ error)
```

Behavior:

- `ok=false, err=nil` when the file does not exist
- `ok=false, err=nil` when the digest does not match
- `ok=false, err=nil` when `artifactVersion` is unsupported
- `ok=false, err=nil` when `state.type != "Module"` or `state.version` is unsupported
- `ok=false, err=nil` when the file is invalid JSON
- `ok=true` only when a valid current artifact was loaded

This keeps the fast path opportunistic and non-breaking.

## Why The Digest Check Is Required

Without a digest check, this would silently load stale typedefs after source
edits.

That would be worse than the current behavior.

The engine already computes the right digest in
[`initializeSDKModule`](../../core/schema/modulesource.go#L3068):

```go
scopedSourceDigest := src.Self().ContentScopedDigest()
srcInstContentHashed := src.WithObjectDigest(digest.Digest(scopedSourceDigest))
```

That is the digest the artifact must store and validate against.

## SDK Impact

None for the steady-state design.

The SDK contract stays exactly the same:

```graphql
moduleTypes(
  modSource: ModuleSource!
  introspectionJSON: File!
  outputFilePath: String!
): Container!
```

Current implementation:

- [`core/sdk/module_typedefs.go`](../../core/sdk/module_typedefs.go)

The engine simply stops calling it when a current saved artifact exists.

That means:

- no new SDK codegen target
- no new SDK entrypoint
- no new SDK helper binary

The existing hook is still required:

- for bootstrap, before the artifact exists
- when the artifact is stale
- for SDKs that do not use `generatedContextDirectory`

## Why This Is Better Than the `baseWithCodegen` Prototype

The smaller `baseWithCodegen` change is still a valid stopgap.

But this design is better because it changes the steady-state cost model:

```text
old steady state:
  dagger functions -> SDK live typedef codegen

new steady state:
  dagger functions -> read dagger.moduletypes.json
```

So this actually moves typedef generation out of runtime.

## Exact Files To Edit

### New

- [`hack/designs/save-load-object-state.md`](./save-load-object-state.md)

### Engine

- [`dagql/stable_state.go`](../../dagql/stable_state.go)
- [`dagql/server.go`](../../dagql/server.go)
- [`core/module_state.go`](../../core/module_state.go)
- [`core/schema/module.go`](../../core/schema/module.go)
- [`core/schema/modulesource.go`](../../core/schema/modulesource.go)

### No SDK Changes Required

- [`core/sdk/go_sdk.go`](../../core/sdk/go_sdk.go) stays unchanged in the final
  design
- [`core/sdk/module_typedefs.go`](../../core/sdk/module_typedefs.go) stays
  unchanged because the fast path happens before it is called

## Tests

Add tests in three groups.

### 1. Artifact write path

New integration test in [`core/integration/module_go_test.go`](../../core/integration/module_go_test.go):

- `generatedContextDirectory` writes `dagger.moduletypes.json`
- the file contains `artifactVersion == 1`
- the file contains the current content-scoped source digest
- the file contains a `Module` state payload

### 2. Fast-path load

New integration tests in [`core/integration/module_go_test.go`](../../core/integration/module_go_test.go):

- when `dagger.moduletypes.json` is current, `dagger functions` does not call
  the Go SDK live typedef path
- when the file is missing, fallback works
- when the digest is stale, fallback works
- when the file is malformed JSON, fallback works

The fallback assertion should be implemented by mutating the artifact or by
removing it from the context and observing that the module still loads.

### 3. Behavioral parity

Round-trip parity test:

1. load a module through current SDK `moduleTypes`
2. save its state to JSON
3. load it back through `__loadModuleFromState`
4. compare:
   - `Description`
   - object names
   - function names
   - argument defaults
   - enum values
   - `+check`
   - `+generate`
   - source maps

Compare semantic fields, not raw `ModuleID` values.

## Rollout

1. land the generic internal state primitive with `Module` support
2. land artifact write support in `runGeneratedContext`
3. land fast-path artifact load in `runModuleDefInSDK`
4. keep SDK `moduleTypes` fallback indefinitely

After that lands, the previous `baseWithCodegen` patch is no longer the target
design. It remains a useful experiment and fallback idea, but it is not the
preferred steady-state architecture.
