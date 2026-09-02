# Module Manifest v2 and Module Entrypoints

Builds on [CLI 1.0: module-max SDK UX](../cli-1.0.md).

Status: draft

## Table of contents

- [Summary](#summary)
- [Problem](#problem)
- [Manifest](#manifest)
- [Module entrypoint](#module-entrypoint)
- [Entrypoint drivers](#entrypoint-drivers)
- [Module manifest builder](#module-manifest-builder)
- [Compatibility](#compatibility)
- [Acceptance tests](#acceptance-tests)

## Summary

Manifest v2 replaces the public module runtime with a module entrypoint.

A module implementation contains the module API code. A module entrypoint
returns the module types and calls the module implementation. An entrypoint
driver is engine code that loads and communicates with a module entrypoint.

An SDK module is a generation tool. It can generate an entrypoint and save it
with the module implementation. The engine does not call the SDK module when
it loads or calls the module.

Use **module entrypoint**, not **module runtime**, for the v2 interface.

This design is not part of PR #13992. It replaces only the runtime and manifest
parts of [CLI 1.0: module-max SDK UX](../cli-1.0.md).

## Problem

The current runtime contract uses an intermediate `Container`. The engine
writes call data to the container, runs it, and reads result data from it. This
container protocol is implicit.

The Dang runtime calls module functions without a container. Thus, a container
is not a required part of the module contract.

## Manifest

The manifest has four required values:

```toml
manifestVersion = 2
name = "hello"

[entrypoint]
kind = "dang"
source = "./internal/dagger/entrypoint"
```

| Field | Meaning |
| --- | --- |
| `manifestVersion` | The file format version. It must be `2`. |
| `name` | The module name. |
| `entrypoint.kind` | The entrypoint kind. It must be `dang` or `module`. |
| `entrypoint.source` | An address that resolves to a `Directory`. |

The engine resolves the source with this operation:

```graphql
address(source).directory()
```

Any address accepted by `Address.directory` is valid. This includes a relative
directory, a local directory, a remote Git directory, or a module function
that returns a `Directory`.

A relative address starts at the module workspace CWD. It does not start at
the host CWD or the engine CWD.

The manifest name is the module name. The entrypoint cannot replace it.

## Module entrypoint

Each entrypoint provides this interface to the engine:

```graphql
"""
Defines and calls one Dagger module.
"""
interface ModuleEntrypoint {
  """Return all types defined by the module."""
  types(
    """The workspace that contains the module implementation."""
    workspace: Workspace!
  ): [TypeDef!]!

  """Call one object constructor or function and return its JSON result."""
  call(
    """The workspace in which to run the call."""
    workspace: Workspace!

    """The original name of the receiver object type."""
    receiverType: String!

    """The receiver state, encoded as JSON."""
    receiverValue: JSON

    """The original function name. An empty string identifies a constructor."""
    fnName: String!

    """
    The function arguments as a JSON object.
    Each key is an original argument name.
    """
    fnArgs: JSON!
  ): JSON!
}
```

The entrypoint and its driver use the same engine session. `Workspace` and
`TypeDef` values are normal Dagger object references. The driver passes their
object IDs. It does not copy these objects between client schemas.

The engine passes the same module workspace to `types` and `call`. The
workspace boundary can be above the module directory. Thus, the entrypoint can
read a file such as `go.mod` above the module directory.

### Type rules

`types` returns every type defined by the module. This includes each object
type that defines a constructor.

The manifest does not define a main type. A type does not need the same name
as the module.

The engine applies the current module type validation. Type names must be
unique.

A function signature can use a name-only `TypeDef` as a type reference. The
engine binds the reference by type kind and original name. It binds a module
type reference to the full definition from `types`. It binds a core type
reference to the target module schema. The reference and its definition do
not need the same Dagger ID.

An object constructor is the `Function` set by `TypeDef.withConstructor`. It
must return its owning object type. Its arguments become the module constructor
arguments.

The initial engine supports zero or one object constructor:

- With zero constructors, the engine installs the types without a module
  constructor.
- With one constructor, the engine exposes it under the installed module name.
- With more than one constructor, the engine returns
  `multiple object constructors are not supported: <names>`.

For `dagger call`, the engine exposes the fields and functions of the object
that the constructor returns. The engine does not call this object a main type.
It does not compare the object name with the module name.

A future engine can expose more object constructors without a manifest change.

### Call rules

The entrypoint uses the current runtime call names:

| Call | `receiverType` | `receiverValue` | `fnName` |
| --- | --- | --- | --- |
| Object constructor | Owning object type | Current empty receiver encoding | Empty string |
| Object function | Receiver object type | Encoded object state | Original function name |

`call` returns the function result. It does not use `FunctionCall`,
`currentFunctionCall`, or a mutable result channel.

`fnArgs` is a JSON object. Each key is an original argument name. Each value
uses the current SDK input encoding. The value is embedded as JSON, not as a
JSON-encoded string. For example:

```json
{
  "name": "World",
  "count": 3,
  "optionalValue": null
}
```

The engine validates arguments and resolves defaults before it creates this
object. A missing key means that the argument was omitted and has no resolved
default. A key with a null value means that the argument value is null. Object
member order has no meaning.

The entrypoint uses the existing 0.x runtime JSON rules:

- `receiverValue` uses the current `FunctionCall.parent` encoding.
- Each `fnArgs` value uses the current argument value encoding.
- The result uses the current `FunctionCall.returnValue` encoding.
- A void result is JSON `null`.
- A failure is a GraphQL error. It is not a JSON result.

The engine can use `ModType.ConvertToSDKInput` for each argument and
`ModType.ConvertFromSDKResult` for the result. It converts a successful JSON
result to the declared return type. A GraphQL error from `call` is the target
function error.

The target function cache policy controls result caching. The engine does not
add a separate result cache for `ModuleEntrypoint.call`.

For an object function, the engine keeps the original receiver node as
internal call context. It keeps this context across the entrypoint call and a
nested dispatch client. `currentNode` returns this node.

A constructor has no receiver node. `currentNode` keeps its current constructor
error. The receiver node is not a public entrypoint argument.

## Entrypoint drivers

Manifest v2 defines two entrypoint drivers.

### `dang`

The `dang` driver is built into the engine. It loads all Dang files in the
entrypoint source directory as one program.

The directory is not a module. It does not need `dagger-module.toml`, and it
cannot declare module dependencies.

Exactly one type must implement `ModuleEntrypoint`. That type must be
constructible as an empty object. The driver creates the object and calls it in
the Dang evaluator.

The entrypoint does not receive an introspection file or an introspection
argument.

### `module`

The `module` driver loads the entrypoint source as a manifest v2 module. Loading
is recursive:

```text
target module
└── module driver
    └── entrypoint module
        └── module driver
            └── entrypoint module
                └── dang driver
```

The entrypoint module must have one constructor. The constructed object must
implement `ModuleEntrypoint`. The constructor must work without supplied
arguments. Optional arguments can use their defaults.

Each driver chain must end at a built-in driver. The engine detects a cycle by
the resolved entrypoint `Directory` ID. A cycle error shows the complete chain.

The engine passes the target module workspace to the entrypoint.

Module schemas are specific to a client. The entrypoint client serves its own
schema. A nested dispatch client serves the target module schema. Both clients
can use the same engine session.

## Module manifest builder

SDK modules use this functional builder. Each `with` function returns a new
value.

```graphql
enum ModuleEntrypointKind {
  DANG
  MODULE
}

type Query {
  """Create a manifest for the current dagger-module.toml format."""
  moduleManifest(name: String!): ModuleManifest!
}

type ModuleManifest {
  """Set the entrypoint kind and source address."""
  withEntrypoint(
    kind: ModuleEntrypointKind!
    source: String!
  ): ModuleManifest!

  """Write the manifest as dagger-module.toml."""
  asFile: File!
}
```

`moduleManifest` sets `manifestVersion` to `2`. `asFile` returns an error until
`withEntrypoint` sets both entrypoint values.

## Compatibility

The presence of `dagger.json` selects the legacy loader. This rule also applies
when `dagger-module.toml` is present.

The legacy loader keeps the current SDK runtime, runtime `Container`, empty
definition call, and introspection behavior.

Without `dagger.json`, `dagger-module.toml` must use manifest version 2. It does
not accept legacy runtime fields.

All new GraphQL fields and types use the v1 schema view gate.

## Acceptance tests

| Area | Required behavior |
| --- | --- |
| Manifest | Read and write the four fields. Reject missing or invalid values. |
| Source | Resolve local, workspace-relative, remote Git, and module-returned directories. |
| Dang driver | Load a directory without a manifest. Require one empty-constructible `ModuleEntrypoint`. Reject dependencies. |
| Types | Bind name-only module and core type references. Reject invalid or duplicate definitions. |
| Constructors | Support zero or one constructor. Use the module name for one constructor. Reject more than one constructor. |
| Calls | Route constructors and functions. Pass arguments as an unordered JSON object. Preserve omitted arguments, null values, and `currentNode`. |
| Module driver | Load one or more recursive drivers. Use the correct client schema. Reject cycles and show the chain. |
| Runtime | Do not call an SDK module or exchange an introspection file during module loading or execution. |
| Compatibility | Select the legacy loader when `dagger.json` exists. |

---

- Previous: [CLI 1.0: module-max SDK UX](../cli-1.0.md)
- Example: [Go SDK](example-go-sdk.md)
- Related: [Compatibility bridge](compat-bridge.md)
