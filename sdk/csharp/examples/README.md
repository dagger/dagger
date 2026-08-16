# C# SDK Examples

This directory contains examples demonstrating both ways to use Dagger with C#:

- **Modules**: reusable Dagger functions served by the engine (`dagger call`, `dagger functions`).
- **Client library**: standalone .NET applications that drive Dagger directly.

Each module example carries a `dagger.json` with `"engineVersion": "latest"` and a
relative SDK ref (`"sdk": { "source": "../.." }`) that resolves to `sdk/csharp`
within this repository, so the examples always run against the working-tree SDK.
Validate any of them with:

```bash
cd <example-dir>
dagger functions
dagger call <function-name> [flags]
```

## Module Examples

### [constructor-example](./constructor-example/)

Constructor parameters as module configuration.

- Default values for all parameters
- Constructor state stored in public properties (required for serialization)
- Constructor args surface as top-level `dagger call` flags / the `with(...)` field

### [factory-example](./factory-example/)

The `[Constructor]` attribute: a static (optionally async) factory method as THE
module constructor.

- Async initialization (`Task<T>` factory)
- Factory parameter names must match public property names (camelCase) so state round-trips

### [defaults-example](./defaults-example/)

Default values and optional parameters.

- String, int, bool, enum defaults
- Nullable reference types (`string?`) and value types (`int?`)
- `[Enum]` / `[EnumValue]` metadata

### [attributes-example](./attributes-example/)

Tour of the attribute surface: `[Object]`, `[Function]`, `[Name]`,
`[DefaultPath]`, `[Ignore]`, `[Deprecated]`, `[Check]`, enum attributes.

- Auto-loading from context directory with `[DefaultPath]`
- Excluding files/patterns with `[Ignore]` (Directory parameters only)
- Renaming functions/fields via `[Function(Name = ...)]`; parameters via `[Name]`
- Checks (`[Function]` + `[Check]`) run by `dagger check`

### [multi-file-example](./multi-file-example/)

Organizing modules across multiple C# files.

- Separation of concerns (Main.cs, Models.cs, Services.cs)
- Custom `[Object]` types returned from and passed to functions
- All `.cs` files compiled together automatically

### [interface-example](./interface-example/) and [processor-impl](./processor-impl/)

Declaring and implementing Dagger interfaces.

- `[Interface(Name = ...)]` type definitions
- `interface-example` declares `Processor` and consumes any implementation
- `processor-impl` structurally implements it (no compile-time dependency)

Note: functions taking interface parameters are not callable from the CLI
(`dagger functions` skips them); call them from another module (see
consumer-example) or via a raw GraphQL query.

### [experimental-example](./experimental-example/)

Using experimental engine APIs (e.g. `Directory.WithPatch`).

- Each experimental API carries a unique diagnostic ID
  (e.g. `DAGGER_DIRECTORY_WITHPATCH`) suppressible with `#pragma warning`

### [consumer-example](./consumer-example/)

Module composition: uses the other examples as dependencies.

- `dagger.json` dependencies with relative local refs
- Cross-module calls via generated bindings on the `Dag` client
- Interface conversion with the generated `As{Module}{Interface}()` method
  (`processorImpl.AsInterfaceExampleProcessor()`)

```bash
cd consumer-example
dagger call run-all
```

## Client Library Usage

### [standalone-client](./standalone-client/)

Using the SDK as a plain library from a console app (no attributes, no module).

- In-repo it builds against `src/Dagger.SDK` via `ProjectReference`;
  standalone apps install the `DaggerIO` NuGet package instead
- Direct API access: `using static Dagger.Client;` then `await Dag.Container()...`

```bash
cd standalone-client
dagger run dotnet run
```

## Comparison

| Feature | Module | Client Library |
|---------|--------|----------------|
| Installation | `dagger init --sdk=csharp` | `dotnet add package DaggerIO` |
| Code Pattern | `[Object]`, `[Function]` attributes | Direct API calls |
| CLI Access | `dagger call function-name` | Run as normal .NET app |
| SDK Code | Generated at runtime into `sdk/` | Referenced assembly |
| Use Case | Reusable CI/CD functions | Standalone applications |

Both patterns use the same underlying Dagger API, just with different entry
points and lifecycle management.
