# C# SDK Examples

This directory contains examples demonstrating both ways to use Dagger with C#:

## Module Usage

Module usage is for creating reusable Dagger functions using the runtime code generation pattern.

### [hello-module](./hello-module/)

A complete example showing how to build, test, and publish a .NET application using Dagger modules.

**Key Features:**

- Build .NET applications
- Run tests
- Create Docker images
- Publish to container registries
- Use `[Object]` and `[Function]` attributes

**Usage:**

```bash
cd hello-module
dagger call build --source=./my-app
dagger call test --source=./my-app
dagger call build-image --source=./my-app
```

### Feature-Specific Examples

#### [constructor-example](./constructor-example/)

Demonstrates constructor parameters as module configuration.

- Default values for all parameters
- Private fields storing constructor state
- Functions using constructor-initialized values

#### [defaults-example](./defaults-example/)

Shows default values and optional parameters.

- String, int, bool, enum defaults
- Nullable reference types (`string?`) and value types (`int?`)
- Complex default configurations

#### [attributes-example](./attributes-example/)

Demonstrates `[DefaultPath]` and `[Ignore]` attributes.

- Auto-loading from context directory
- Excluding files/patterns efficiently
- Multiple directories with different patterns

#### [multi-file-example](./multi-file-example/)

Shows how to organize modules across multiple C# files.

- Separation of concerns (Main.cs, Models.cs, Services.cs)
- Builder patterns, validators, services
- All `.cs` files compiled together automatically

#### [interface-example](./interface-example/)

Demonstrates defining and implementing interfaces.

- `[Interface]` attribute for type definitions
- Multiple implementations of same interface
- Fluent API patterns with interfaces

#### [consumer-example](./consumer-example/)

Shows using other modules as dependencies.

- Module composition with `dagger.json` dependencies
- Cross-module function calls via `Dag` client
- Real-world module integration patterns

## Client Library Usage

Client library usage is for writing standalone applications that use Dagger as a library (via NuGet package).

### [standalone-client](./standalone-client/)

Examples of using DaggerIO as a NuGet package in standalone C# applications.

**Key Features:**

- Install via `dotnet add package DaggerIO`
- No attributes needed
- Direct API access via `using Dagger;` then `await Dag.Container()...`
- Use in any .NET application

**Usage:**

```bash
cd standalone-client
dotnet run
```

## Comparison

| Feature | Module | Client Library |
|---------|--------|----------------|
| Installation | `dagger init --sdk=csharp` | `dotnet add package DaggerIO` |
| Code Pattern | `[Object]`, `[Function]` attributes | Direct API calls |
| CLI Access | `dagger call function-name` | Run as normal .NET app |
| SDK Code | Generated at runtime | Pre-generated in NuGet package |
| Use Case | Reusable CI/CD functions | Standalone applications |

## Getting Started

1. **For Module development**: Start with `hello-module` to see basic patterns, then explore feature-specific examples
2. **For Client applications**: Start with `standalone-client` to see how to use Dagger as a library

Both patterns use the same underlying Dagger API, just with different entry points and lifecycle management.
