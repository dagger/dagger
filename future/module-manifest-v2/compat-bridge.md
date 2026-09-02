# Module Manifest v2 Compatibility Bridge

> [!WARNING]
> This is an experimental draft. It is not ready for implementation.

Builds on [Module Manifest v2 and Module Entrypoints](spec.md).

status: draft spec
created: 2026-09-01

## Table of contents

- [Purpose](#purpose)
- [Terms](#terms)
- [Problem](#problem)
- [Non-goals](#non-goals)
- [Decision](#decision)
- [Function call channel](#function-call-channel)
- [Legacy entrypoint module](#legacy-entrypoint-module)
- [SDK resolution](#sdk-resolution)
- [Dependencies](#dependencies)
- [Manifest rules](#manifest-rules)
- [Cache behavior](#cache-behavior)
- [Error rules](#error-rules)
- [Implementation outline](#implementation-outline)
- [Acceptance tests](#acceptance-tests)
- [Status](#status)

## Purpose

This design moves the `dagger.json` loader out of the engine. A legacy module
loads through manifest v2 and a legacy entrypoint module.

The engine keeps one module loading path. The legacy protocol survives as a
container feature that only the legacy entrypoint module uses.

## Terms

Use these terms with the exact meanings in this table.

| Term | Meaning |
| --- | --- |
| Legacy module | A module that has a `dagger.json` file and a 0.x SDK runtime. |
| Legacy runtime | The `Container` that a 0.x SDK returns from `moduleRuntime`. Its entrypoint reads `currentFunctionCall` and writes `returnValue`. |
| Legacy entrypoint module | The v2 module that implements `ModuleEntrypoint` for legacy modules. |
| Function call channel | A per-exec value. The nested session serves it as `currentFunctionCall`. `returnValue` and `returnError` write into it. |

## Problem

1. **Two loaders** - Manifest v2 keeps `Runtime`, `ModuleTypes`,
   `ModuleRuntime`, and `ContainerRuntime` in the engine for `dagger.json`
   compatibility.
2. **The legacy protocol is not public** - `ContainerRuntime.Call` in
   `core/sdk.go` passes a `FunctionCall` into the exec through an internal
   argument. See `core/container_exec.go:1187`. No public API can do this.
3. **Built-in SDK resolution is engine-only** - The legacy Go runtime is
   engine code. See `core/sdk/go_sdk.go:297`. The Python and TypeScript
   runtimes load from the engine image. See `core/sdk/loader.go:198`.
4. **Dependencies come from the module context** - A nested exec gets its
   schema from the dependencies of the active module. See
   `engine/server/session.go:936`. `Module.serve` in the caller session does
   not reach the nested exec.

## Non-goals

1. Do not change any 0.x SDK runtime binary.
2. Do not support runtime code generation. A legacy module must have its
   generated code committed.
3. Do not load a legacy module that has no v2 manifest.
4. Do not show the function call channel in the v2 schema view.
5. Do not add language drivers. This is non-goal 2 of manifest v2.

## Decision

A legacy module gets a v2 manifest. The manifest selects the legacy entrypoint
module with the `module` kind:

```toml
manifestVersion = 2
name = "hello"

[entrypoint]
kind = "module"
source = "github.com/dagger/dagger/modules/legacy-entrypoint@v1.0.0"
```

The engine adds one gated API: the function call channel on `Container`.

The engine removes the `dagger.json` loader, `Runtime`, `ModuleTypes`,
`ModuleRuntime`, and `ContainerRuntime`.

## Function call channel

The engine exposes these fields in the legacy schema view only:

```graphql
type Container {
  """
  Attach a function call to the next exec.

  The nested session of that exec serves this value from
  Query.currentFunctionCall. The exec writes its result with
  FunctionCall.returnValue or FunctionCall.returnError.

  An empty name requests the module definition. This is the 0.x convention.
  """
  withFunctionCall(
    """The name of the function. Empty for the module definition call."""
    name: String!

    """The name of the receiver type. Empty for the main object."""
    parentName: String

    """The JSON-encoded receiver state."""
    parent: JSON!

    """The function arguments as a JSON object in the v2 call codec."""
    inputArgs: JSON!
  ): Container!

  """
  Return the value that the last exec wrote with returnValue.

  Reports an error if the exec wrote returnError, or if the exec did not
  write a result.
  """
  functionCallResult: JSON!
}
```

The engine converts `inputArgs` to the existing internal
`FunctionCallArgValue` values before the nested exec. Object member order has
no meaning.

The channel has the scope of one exec. It has no link to the engine's own call
state:

- The engine does not create a `FunctionCall` for a v2 `call`. Inside a v2
  `call`, `currentFunctionCall` reports an error unless `withFunctionCall`
  set a channel.
- A `returnValue` write inside a nested exec changes only the channel of that
  exec. It does not change the result of the outer v2 `call`.
- The receiver in `parent` is JSON only. `Query.currentNode` still returns the
  typed receiver that the v2 engine binds during `call`.

The existing `FunctionCall`, `currentFunctionCall`, `returnValue`, and
`returnError` types move to the legacy schema view with the two new fields.

## Legacy entrypoint module

The legacy entrypoint module implements `ModuleEntrypoint`. It is a normal v2
module. It uses the public API and the function call channel.

### `types`

1. Read `dagger.json` from the module workspace.
2. Build a `ModuleSource` with `Directory.asModuleSource`.
3. Resolve the SDK module. See [SDK resolution](#sdk-resolution).
4. Call `moduleRuntime(modSource:)` on the SDK module. Do not pass
   `introspectionJson`.
5. Exec the container with an empty function call channel.
6. Read `functionCallResult`. The value is a `ModuleID`.
7. Load the module and return `objects`, `interfaces`, and `enums` as one
   `[TypeDef!]!` list.

### `main`

Return the object definition whose name equals the manifest name. This is the
0.x rule for the main object.

### `call`

1. Build the same runtime container as `types`.
2. Set the channel with `withFunctionCall(name: fnName, parentName:
   receiverType, parent: receiverValue, inputArgs: fnArgs)`.
3. Exec the container.
4. Return `functionCallResult`.

The v2 `call` codec is the 0.x codec. The values pass through without
conversion.

## SDK resolution

The legacy entrypoint module maps the `sdk.source` value of `dagger.json` to a
runtime source:

| `sdk.source` | Runtime source |
| --- | --- |
| `go` | Go runtime implemented in the legacy entrypoint module. |
| `python` | `github.com/dagger/dagger/sdk/python/runtime@<tag>` |
| `typescript` | `github.com/dagger/dagger/sdk/typescript/runtime@<tag>` |
| Other built-in name | The git reference that `core/sdk/loader.go` maps it to. |
| Git or local reference | The reference as written. |

`<tag>` is the engine version of the legacy entrypoint module release. A
legacy module that needs an older runtime pins an older legacy entrypoint
module in its manifest.

The Go runtime is the only runtime that needs new module code. Its 0.x
implementation is the container build in `core/sdk/go_sdk.go`. The legacy
entrypoint module implements the same build with the public `Container` API.

## Dependencies

A legacy `dependencies` list in `dagger.json` becomes a list of installed
modules in the workspace `dagger.toml`. The migration writes this list.

The engine puts the installed modules in the dependencies of the target
module. The nested exec then sees them in its schema, and the engine uses them
to resolve dependency types in `types`.

This rule depends on a manifest v2 decision that is not yet written: how a v2
module references types from installed modules. This document assumes that the
answer is the workspace `dagger.toml`.

## Manifest rules

Manifest v2 rejects a directory that contains both `dagger.json` and
`dagger-module.toml`. This design amends that rule:

- If `dagger-module.toml` has `manifestVersion = 2`, the engine ignores
  `dagger.json`. Only the legacy entrypoint module reads it.
- The reason for the old rule was an ambiguous compatibility mode. With this
  design, the engine has no compatibility mode.

The manifest v2 rule that the `module` driver does not load a `dagger.json`
entrypoint module stays. The legacy entrypoint module is a v2 module. Only the
wrapped module is legacy.

## Cache behavior

The runtime container build depends on the module workspace content and the SDK
runtime source. It does not depend on the function call.

The exec identity includes the function call channel. A new call reuses the
built container and runs one new exec. This is the same shape as the 0.x
cache.

## Error rules

The engine and the legacy entrypoint module must report these errors:

- `functionCallResult` is read after an exec that wrote no result.
- `functionCallResult` is read after an exec that wrote `returnError`. The
  error carries the `Error` value.
- `currentFunctionCall` is called in a v2 `call` that set no channel.
- `dagger.json` is missing from the module workspace.
- `dagger.json` names an SDK that does not resolve.
- `dagger.json` requests runtime code generation and the module has no
  committed generated code.
- The empty-call result is not a `ModuleID`.
- The module definition has no object with the manifest name.

## Implementation outline

1. Add `withFunctionCall` and `functionCallResult` to `Container` in the
   legacy schema view.
2. Move `FunctionCall`, `currentFunctionCall`, `returnValue`, and
   `returnError` to the legacy schema view.
3. Write the legacy entrypoint module as a v2 module. Implement `types`,
   `main`, and `call`.
4. Implement the Go runtime build in the legacy entrypoint module.
5. Add the built-in SDK name table.
6. Add a migration command that writes `dagger-module.toml` and the installed
   module list for a legacy module.
7. Route all `dagger.json` modules in the test suite through the legacy
   entrypoint module.
8. Remove the `dagger.json` loader, `Runtime`, `ModuleTypes`,
   `ModuleRuntime`, and `ContainerRuntime` from the engine.

## Acceptance tests

1. Load and call a Go, Python, and TypeScript legacy module through the legacy
   entrypoint module.
2. Load a legacy module with a git SDK reference.
3. Load a legacy module with one dependency and call a dependency function.
4. Return a dependency type from a legacy module function.
5. Verify that `currentFunctionCall` fails in a v2 `call` with no channel.
6. Verify that `returnValue` in a nested exec does not change the outer v2
   `call` result.
7. Verify that `currentNode` returns the typed receiver inside a legacy
   runtime.
8. Propagate `returnError` from a legacy runtime as the target function error.
9. Reject a legacy module that requests runtime code generation.
10. Verify that a second call reuses the runtime container build.
11. Load a directory that has both manifest files with `manifestVersion = 2`.
12. Verify that the v2 schema view does not show the function call channel.

## Status

Draft design. No implementation is part of this document.

---

- Previous: [Module Manifest v2 and Module Entrypoints](spec.md)
- Next: implementation plan (coming soon)
