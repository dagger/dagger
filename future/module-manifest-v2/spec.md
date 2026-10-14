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

The manifest has three required values:

```toml
name = "hello"

[entrypoint]
kind = "dang"
source = "./internal/dagger/entrypoint"
```

| Field | Meaning |
| --- | --- |
| `name` | The module name. |
| `entrypoint.kind` | The entrypoint kind. It must be `dang`. |
| `entrypoint.source` | A local path, or an address that resolves to a `Directory`. |

### Format selection

The presence of the `entrypoint` table selects this format. A
`dagger-module.toml` that has an `entrypoint` table is a version 2 manifest. A
`dagger-module.toml` that has no `entrypoint` table is the previous format.
There is no `manifestVersion` key.

One transitional rule qualifies this. Version 2 has no dependency list, and the
SDKs still need one, so a dependency list keeps a manifest on the previous
format even when it declares an entrypoint:

| `entrypoint` | `dependencies` | `runtime` | Format |
| --- | --- | --- | --- |
| absent | any | any | Previous |
| present | absent | any | Version 2, previous fields ignored |
| present | present | present | Previous, entrypoint ignored |
| present | present | absent | Error, neither format can load it |

A manifest that carries an entrypoint table and the previous fields at the same
time is a fat manifest. It loads on an engine that supports entrypoints and on
an engine that does not, because the older engine ignores the entrypoint table.
A version 2 read ignores the previous fields rather than rejecting them.

This rule disappears once version 2 covers dependencies. The engine keeps it in
`core/modules/config_fat_manifest.go`, which names the steps to remove it.

A version 2 manifest accepts `name` and `entrypoint` and nothing else, so a
`manifestVersion` key is rejected as an unsupported key. An earlier draft of
this design required that key, but no released engine ever read or wrote it,
so no manifest in the wild carries it.

The engine rejects a `dagger.json` that sets `entrypoint`. The `entrypoint`
table belongs to `dagger-module.toml` only, so a legacy file can never select
this format.

**Decision: a future format version needs a structural selector.** This format
reads no version number, so a version 3 cannot announce itself with one. It
must differ by structure, such as a new required table, or by a new file name.
This design accepts that cost. The alternative keeps a version key that every
manifest repeats and that no manifest can disagree with.

**Decision: an engine that predates this design ignores the entrypoint.** Such
an engine decodes `dagger-module.toml` with a decoder that drops unknown keys.
It therefore ignores the `entrypoint` table, finds no `runtime` value, and
fails with `no sdk ref provided`. That engine drops a `manifestVersion` key for
the same reason, so it fails the same way for a manifest that carries one.
Removing the version key does not change how often this failure happens. This
design accepts the failure and adds no version handshake.

A local path is relative to the directory that contains `dagger-module.toml`.
It must stay inside that directory. An absolute path is an error. It does not
start at the host CWD, the workspace CWD, or the engine CWD. The engine reads
the path from the module source context, so it resolves the same way for a
local, Git, or directory module source.

A value that is not a local path is a module reference or an address.

A module reference names a directory in a Git repository. It uses the syntax
that `runtime.source` and a dependency `source` use, such as
`github.com/dagger/python-sdk/entrypoint@v1` or
`https://github.com/dagger/python-sdk#v1:entrypoint`. The engine resolves it
with the module source resolver, so it takes part in the workspace lock like
any other module reference. The reference names the entrypoint directory
itself. The engine does not search parent directories for a manifest, and the
directory needs none.

An address is resolved with this operation:

```graphql
address(source).directory()
```

Any address accepted by `Address.directory` is valid. This includes a module
function that returns a `Directory`.

The engine classifies the value in this order:

1. A value that starts with `.` or `/` is a local path.
2. A value that contains `://` is a module reference.
3. Any other value that contains `:`, such as a `module:function` address, is
   an address.
4. A value with no `.` is a local path.
5. Any other value is a local path when it exists under the module directory,
   and a module reference otherwise.

Rule 5 depends on what exists on disk, and the module source resolver also
checks the caller's filesystem for a value with no scheme. A value with a
scheme, such as `https://`, is decided by rule 2 and never touches the
filesystem. Use a scheme when the value must be unambiguous.

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
read a file such as `go.mod` above the module directory. A file that the
entrypoint reads from the workspace is not part of the module digest, so a
function result with the default cache policy does not follow a change to it.
Return such files from `sources` instead.

### Module sources

The entrypoint type can declare one optional member, `sources`. Declaring it
selects a second interface:

```graphql
interface ModuleEntrypoint {
  """
  Return every file the module builds from, at its path from the root of
  the workspace. The workspace working directory is the module directory.
  """
  sources(workspace: Workspace!): Directory!

  types: [TypeDef!]!

  call(
    receiverType: String!
    receiverValue: JSON
    fnName: String!
    fnArgs: JSON!
  ): JSON!
}
```

The engine calls `sources` while it loads the module source, before anything
reads the module digest. The module context becomes the returned directory
plus the module's own files: its manifest, its source directory and its
`include` paths, as the engine loads them without the member. The engine then
removes every `.git` directory and file: `.git/config` can hold credentials,
and `.git` changes on every fetch. The returned files are thus part of
`contextDirectory` and of the module digest, so they reach the cache key of
every function call and of `types`. A file outside the returned directory does
not change the context digest. The digest also covers the module identity, its
paths, its user defaults and its SDK, as for any module.

The returned directory must hold the module manifest at the module path, as
in this example:

```dang
pub sources(workspace: Workspace!): Directory! {
  directory
    .withDirectory("lib", workspace.directory("/lib", include: ["go.mod", "greet/*.go"], gitignore: true))
    .withDirectory(workspace.cwd, workspace.directory(".", gitignore: true))
}
```

The engine refuses a directory without the manifest at the module path, which
also catches a directory rooted at the module directory.

`types` and `call` get no workspace under this interface. They read the module
through `currentModule`. Dang accepts an extra nullable argument, so an
entrypoint that also supports an engine without `sources` can keep a nullable
`workspace` argument on `types` and `call`, and can tell such an engine by the
workspace it receives.

The workspace that `sources` gets reads the module's own source:

| Module source | Workspace |
| --- | --- |
| Local | The checkout on the host, rooted at the context root, read through the client that loaded the module. This holds for a module outside the caller's workspace. |
| Loaded through `Workspace.moduleSource` | That workspace. |
| Git | The tree of the module commit. |
| Directory | The directory that the caller passed. |

A Git module thus never reads the caller's checkout.

The module's own files keep their `.gitignore` rules: a local module context
applies `.gitignore`, and a Git or directory module context does not. The
returned directory follows the `gitignore` argument that `sources` passes to
`Workspace.directory`. A directory module context holds the module's own files
and the returned files, not the whole directory that its caller passed. This
also holds for a module that another module serves from its own tree.

The engine calls `sources` once for each load of the module source, which is
once for each client, and once for each function call of a module that
declares the module as a client. Keep it to a few API calls: a `glob` over the
workspace or one read per file multiplies the load cost.

`sources` runs with a nested client like a module function: it has the Dagger
API, and it reaches the caller's host only through the workspace it gets. It
runs before the module exists, so it cannot read `currentModule`. A module that
declares `sources` gets the same trust as its function code.

The engine evaluates `sources` only when a type that implements
`ModuleEntrypoint` in a top-level `.dang` file of the entrypoint directory
declares a public member with that name. A comment, a string, a private member
or a member of another type does not count. `sources` needs a local
entrypoint: an entrypoint named by a module reference or an address that
declares the member fails when the module loads its types.

### Module context in the entrypoint

The entrypoint reads the module context through `currentModule`:

| Field | Value |
| --- | --- |
| `source` | The module directory, with generated files applied. |
| `contextDirectory` | The module context at the context root, with generated files applied. |
| `sourceSubpath` | The path of the module directory inside `contextDirectory`. |

An SDK that resolves relative paths, such as a path dependency on
`../shared`, mounts `contextDirectory` and builds from `sourceSubpath` inside
it. The workspace cannot replace these fields: a Git or directory module is not
in the workspace, and the module digest does not cover workspace files.

For a local or Git module, `contextDirectory` holds the manifest, the module
directory and the files that `sources` returns, at their paths from the context
root. A local module context applies `.gitignore`. A directory module source
without `sources` keeps the whole directory that its caller passed.

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

Manifest v2 defines one entrypoint driver. A module is not an entrypoint: a
runtime implemented as a module is named by `runtime.source` in the previous
format, and the engine drives it as a runtime.

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

### Observability

The engine records which interface it drove a module through, so the choice
is not silent. The span is internal: it stays out of ordinary output and
appears when inspecting a trace, or at `-vvv`.

| Span | Meaning |
| --- | --- |
| `module entrypoint interface` | `ModuleEntrypoint`, called directly. |
| `legacy runtime interface` | A runtime named by `runtime.source`, with no entrypoint in play. |

A fat manifest carries both keys, so exactly one of these says which one the
engine honored.

## Module manifest builder

SDK modules use this functional builder. Each `with` function returns a new
value.

```graphql
enum ModuleEntrypointKind {
  DANG
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

`asFile` writes `name` and the `entrypoint` table, and nothing else. It returns
an error until `withEntrypoint` sets both entrypoint values. It writes no
`manifestVersion` key, because the `entrypoint` table selects the format.

## Compatibility

The presence of `dagger.json` selects the legacy loader. This rule also applies
when `dagger-module.toml` is present.

The legacy loader keeps the current SDK runtime, runtime `Container`, empty
definition call, and introspection behavior.

Without `dagger.json`, `dagger-module.toml` must use manifest version 2, so it
must have an `entrypoint` table. It does not accept legacy runtime fields.

That rule describes the end state. Until the migration finishes, the engine
still reads a `dagger-module.toml` with no `entrypoint` table as the previous
format. See [Format selection](#format-selection).

All new GraphQL fields and types use the v1 schema view gate.

## Acceptance tests

| Area | Required behavior |
| --- | --- |
| Manifest | Read and write the three fields. Select the format by the presence of the `entrypoint` table. Reject missing or invalid values. Reject a `manifestVersion` key in a version 2 manifest and an `entrypoint` value in `dagger.json`. |
| Context | Expose the module context and the module subpath through `currentModule.contextDirectory` and `currentModule.sourceSubpath`. Keep `currentModule.source` narrowed to the module directory. |
| Sources | Evaluate a declared `sources` member while the module source loads, with the module's own workspace, and add the returned files to the module context. Pass no workspace to `types` and `call` of such an entrypoint. Load a module without the member as before. Reject a directory without the module manifest at the module path, and the member on a non-local entrypoint. |
| Source | Resolve module-relative local paths, module references, and module-returned directories. Keep a module reference on the directory it names. Reject paths that leave the module directory. |
| Dang driver | Load a directory without a manifest. Require one empty-constructible `ModuleEntrypoint`. Reject dependencies. |
| Types | Bind name-only module and core type references. Reject invalid or duplicate definitions. |
| Constructors | Support zero or one constructor. Use the module name for one constructor. Reject more than one constructor. |
| Calls | Route constructors and functions. Pass arguments as an unordered JSON object. Preserve omitted arguments, null values, and `currentNode`. |
| Runtime | Do not call an SDK module or exchange an introspection file during module loading or execution. |
| Compatibility | Select the legacy loader when `dagger.json` exists. |

---

- Previous: [CLI 1.0: module-max SDK UX](../cli-1.0.md)
- Example: [Go SDK](example-go-sdk.md)
