# Dagger Codegen Reference

Use this as orientation when editing generated SDK bindings, module runtime
dispatch, SDK module interfaces, or generated clients. Verify details against
current code before changing behavior; these notes describe the current model,
but codegen paths move often.

For schema-view compatibility and `engineVersion` gates that affect generated
output, also read `version-gating.md`.

## Four Codegen Meanings

`codegen` can refer to several different surfaces:

| Surface | Trigger | Main output | Main code |
|---------|---------|-------------|-----------|
| In-module bindings | `dagger develop`, `dagger call`, generated context loading | SDK-specific module source changes such as Go `internal/dagger/dagger.gen.go` | `core/schema/modulesource.go`, `core/sdk.go`, `core/sdk/*` |
| Runtime dispatch | Module execution | Dispatch entrypoint that calls user functions | Go: `cmd/codegen/generator/go/templates/modules.go`; TypeScript: `cmd/codegen generate-entrypoint` |
| SDK libraries | Repo SDK generation | Shipped SDK packages such as `sdk/go/dagger.gen.go` | `cmd/codegen generate-library`, `sdk/go/generate.go`, SDK-specific generators |
| Generated clients | `dagger client install` / configured module clients | Client bindings for regular programs outside module runtime | `ClientGenerator`, `cmd/dagger/client.go`, `cmd/codegen generate-client` |

Decide which surface you are editing before changing templates. Similar output
files may be produced for different modes.

## Engine SDK Interfaces

The engine-facing SDK interfaces are in `core/sdk.go`.

`CodeGenerator` supports language SDK codegen for module sources:

```go
Codegen(
    context.Context,
    *SchemaBuilder,
    dagql.ObjectResult[*ModuleSource],
) (*GeneratedCode, error)
```

Module SDKs expose this through a GraphQL function:

```graphql
codegen(modSource: ModuleSource!, introspectionJson: File!): GeneratedCode!
```

`ClientGenerator` supports standalone generated clients:

```go
RequiredClientGenerationFiles(context.Context) (dagql.Array[dagql.String], error)
GenerateClient(
    context.Context,
    dagql.ObjectResult[*ModuleSource],
    dagql.Result[*File],
    string,
) (dagql.ObjectResult[*Directory], error)
```

Module SDKs expose this through GraphQL functions:

```graphql
requiredClientGenerationFiles: [String]!
generateClient(
    modSource: ModuleSource!
    introspectionJson: File!
    outputDir: String!
): Directory!
```

`Runtime` returns a `ModuleRuntime`, which may be a container-backed runtime or
another runtime implementation. Container-backed SDK modules expose
`moduleRuntime(modSource: ModuleSource!, introspectionJson: File!): Container!`.

`ModuleTypes` asks an SDK to produce typedefs for user module code. Module SDKs
expose `moduleTypes(modSource: ModuleSource!, introspectionJson: File!,
outputFilePath: String!): Container!`.

## Built-In SDK Shape

The built-in SDK list is defined in `core/sdk/consts.go`:

- `go`
- `dang`
- `python`
- `typescript`
- `php`
- `elixir`
- `java`

Go is special. `core/sdk/go_sdk.go` implements the SDK directly in the engine by
running the packaged `cmd/codegen` binary in a Go runtime container. It
implements runtime, module types, codegen, and client generation without going
through a module SDK wrapper.

The other built-ins are module SDKs. `core/sdk/module.go` loads the SDK module,
records which SDK functions it implements, and adapts those functions to the
engine interfaces through:

- `core/sdk/module_code_generator.go`
- `core/sdk/module_client_generator.go`
- `core/sdk/module_runtime.go`
- `core/sdk/module_typedefs.go`

`sdk/dotnet` and `sdk/rust` contain SDK-related code, but they are not in
`validInbuiltSDKs`; do not treat them as built-in SDKs unless that list changes.

## Generated Context Flow

`core/schema/modulesource.go` is the main generated-context path:

1. `runCodegen` loads dependency modules, checks whether the current SDK
   implements `CodeGenerator`, and calls `Codegen`.
2. If the SDK supports `ModuleTypes` and the explicit self-call experimental
   flag is enabled, the current module is added to codegen deps so its own types
   can be generated.
3. The returned `GeneratedCode` supplies the generated directory plus
   `VCSGeneratedPaths` and `VCSIgnoredPaths`; the engine updates
   `.gitattributes` and `.gitignore` accordingly.
4. `runClientGenerator` handles configured clients. It loads the requested
   generator SDK, asks for optional required files, builds a client-facing schema
   with dependencies and entrypoint self bindings when available, and merges the
   generated client directory into the generated context.
5. `runGeneratedContext` runs codegen, runs configured clients, then writes the
   module config into the generated context.

## `cmd/codegen`

`cmd/codegen` currently has generator backends for:

- `go`
- `typescript`

The command supports:

- `generate-module`
- `generate-client`
- `generate-library`
- `generate-typedefs`
- `generate-entrypoint`

The `Generator` interface lives in `cmd/codegen/generator/generator.go`.
`generator.Config` in `cmd/codegen/generator/config.go` carries mode-specific
config for module generation, standalone client generation, and entrypoint
generation.

The TypeScript SDK runtime uses the same `cmd/codegen` binary for generated
module/client/library pieces and for static entrypoint rendering. Other SDKs
have their own codegen implementations under their SDK directories.

## Go Templates

Go templates live under `cmd/codegen/generator/go/templates/src/`:

```text
src/
|-- dagger.gen.go.tmpl
|-- dag/
|   `-- dag.gen.go.tmpl
|-- internal/dagger/
|   |-- dagger.gen.go.tmpl
|   `-- _dep.gen.go.tmpl
|-- _dagger.gen.go/
|   |-- client.go.tmpl
|   |-- defs.go.tmpl
|   `-- module.go.tmpl
`-- _types/
    |-- enum.go.tmpl
    |-- input.go.tmpl
    |-- interface.go.tmpl
    |-- legacy_interface.go.tmpl
    |-- object.go.tmpl
    |-- object_fields.go.tmpl
    `-- scalar.go.tmpl
```

Important helper functions are exposed by
`cmd/codegen/generator/go/templates/functions.go`:

- `IsModuleCode`: module generation mode.
- `IsStandaloneClient`: generated-client mode.
- `IsPartial`: first pass of two-pass Go module generation.
- `ModuleMainSrc`: generated Go module `main` and `invoke` dispatch.
- `IsArgOptional` and `HasOptionals`: optional argument checks. Use these
  helpers instead of direct nullable checks because defaults and schema-version
  compatibility matter.
- `CheckVersionCompatibility`: template behavior gated by schema version.
- `LegacyGoSDKCompat`: compatibility mode for pre-cutover Go SDK output.

Go module generation may run twice. The first pass can create `go.mod`, initial
`dagger.gen.go`, and starter `main.go`. After the package can be loaded, the
second pass renders full bindings and static `invoke` dispatch.

Go codegen splits output by dependency:

- Most files render against the core schema with dependency types excluded.
- Files that need dependency Query fields, such as `dag/dag.gen.go`, render
  against the full schema.
- Dependency-contributed types are emitted as per-dependency files under
  `internal/dagger/` or under the generated client directory.

## Generated Clients

`dagger client` is hidden and annotated experimental in `cmd/dagger/client.go`.
`dagger client install <generator> [path]` updates module client config by
calling `ModuleSource.WithClient(...).GeneratedContextDirectory().Export(...)`.

The command is generator-agnostic, but the selected SDK must implement
`ClientGenerator`. In the current built-in set, Go and TypeScript implement
client generation.

For Go generated clients:

- `cmd/codegen generate-client` reads module source dependency metadata when a
  module source ID is provided.
- `cmd/codegen/generator/go/generate_client.go` supports legacy root-client
  generation and the newer separate client module directory.
- Separate client modules get their own `go.mod`; released engine versions pin
  `dagger.io/dagger` in that client module unless a custom replace overrides it.
- The generated client directory is tidied with `go mod tidy`; the parent module
  is not tidied automatically.

Generated clients differ from in-module bindings because they include explicit
connection helpers and dependency serving for use by ordinary programs.

## Common Edits

| To change | Start with |
|-----------|------------|
| Engine SDK interfaces | `core/sdk.go`, then the Go and module SDK adapters |
| Generated method signatures | `cmd/codegen/generator/go/templates/src/_types/object.go.tmpl` and helpers in `functions.go` |
| Go module runtime dispatch | `cmd/codegen/generator/go/templates/modules.go` |
| Standalone Go client `Connect` / `Close` | `cmd/codegen/generator/go/templates/src/_dagger.gen.go/client.go.tmpl` |
| TypeScript generated code | `cmd/codegen/generator/typescript/` and `sdk/typescript/runtime/` |
| Python generated code | `sdk/python/codegen/src/codegen/` |
| Built-in SDK list | `core/sdk/consts.go` |

When changing templates, rebuild or regenerate through the repo's normal
generation path before judging output.
