# Dagger C# SDK

A complete C# SDK for [Dagger](https://dagger.io/) with support for creating modules and building custom CI/CD pipelines.

## Features

- 🎯 **Module Development**: Create reusable Dagger modules
- 🔍 **Roslyn Analyzers**: Compile-time validation and code fixes for common issues
- 📚 **XML Docs**: XML Docs are used for dagger function docs

## What is the Dagger C# SDK?

The Dagger C# SDK provides two complementary ways to work with Dagger:

### 🔧 Module Development

Create reusable Dagger modules that can be called by other modules or from the CLI:

```bash
dagger init --sdk=csharp --name=my-module
```

Write functions decorated with `[Object]`, `[Function]`, `[Field]`, `[Interface]` attributes. 
The SDK source code is generated into your module's `sdk/` directory for version consistency.

**Use this for:** Reusable CI/CD functions, shareable build tools, extending Dagger's ecosystem

### 📦 Client Library

Use Dagger programmatically in any .NET application via NuGet:

```bash
dotnet add package DaggerIO
```

Build custom pipelines by importing the SDK and calling the Dagger API directly.

**Use this for:** Custom build scripts, automation tools, one-off pipeline tasks

## Documentation

- **[ARCHITECTURE.md](./ARCHITECTURE.md)** - SDK architecture and internals
- **[PUBLISHING.md](./PUBLISHING.md)** - Guide for publishing the SDK
- **[examples/](./examples/)** - Example modules and standalone clients
- **[Analyzers Documentation](./src/Dagger.SDK.Analyzers/README.md)** - Roslyn analyzer features

## Requirements

- .NET 10.0 LTS or later
- [Docker](https://docs.docker.com/engine/install/), or another OCI-compatible container runtime
- [Dagger CLI](https://docs.dagger.io/cli) v0.19.0 or later

---

## Getting Started

### Quick Start with Modules

Initialize a new C# module:

```bash
dagger init --sdk=csharp --name=my-module
cd my-module
```

The generated module includes:
- `Main.cs` - Your module class with Dagger functions
- `Program.cs` - Entrypoint that bootstraps the SDK runtime
- `MyModule.csproj` - Project file
- `sdk/` - Generated Dagger SDK source code

Edit `Main.cs` to add your functions:

```csharp
using Dagger;

[Object]
public class MyModule
{
    /// <summary>
    /// Returns a container that echoes a message
    /// </summary>
    [Function]
    public Container Echo(string message)
    {
        return Dag
            .Container()
            .From("alpine:latest")
            .WithExec(new[] { "echo", message });
    }
}
```

Test your module:

```bash
dagger call echo --message="Hello from Dagger!"
```

---

## 🔧 Module Development

### Attributes

The SDK uses attributes to define module structure:

- `[Object]` - Marks a class as a Dagger module object
- `[Function]` - Marks a method as a callable Dagger function  
- `[Field]` - Marks a property as a Dagger field
- `[Interface]` - Marks an interface for polymorphic behavior

### Example Module

```csharp
using Dagger;

[Object]
public class MyModule
{
    /// <summary>
    /// Returns a container that echoes a message
    /// </summary>
    [Function]
    public Container Echo(string message)
    {
        return Dag
            .Container()
            .From("alpine:latest")
            .WithExec(new[] { "echo", message });
    }

    /// <summary>
    /// Builds and tests a Go project
    /// </summary>
    [Function]
    public async Task<string> BuildAndTest(Directory source)
    {
        return await Dag
            .Container()
            .From("golang:1.21")
            .WithMountedDirectory("/src", source)
            .WithWorkdir("/src")
            .WithExec(new[] { "go", "build", "./..." })
            .WithExec(new[] { "go", "test", "./..." })
            .Stdout();
    }
}
```

### Interface Support

Dagger supports interfaces for polymorphic module behavior using structural typing. Modules can define interfaces and accept any implementation that matches the interface's method signatures.

--- 
### MyInterfaceExample.csproj

```csharp
using System.Threading.Tasks;
using Dagger;

[Interface]
public interface IProcessor
{
    [Function]
    Task<string> Process(string input);
}

[Object]
public class InterfaceExample
{
    [Function]
    public async Task<string> ProcessText(IProcessor processor, string text)
    {
        return await processor.Process(text);
    }
}
```

--- 
### MyImplementation.csproj

```csharp
[Object]
public class InterfaceImplementation
{
    [Function]
    public Task<string> Process(string text)
    {
        // Simple implementation that reverses the input text
        char[] charArray = text.ToCharArray();
        Array.Reverse(charArray);
        return Task.FromResult(new string(charArray));
    }
}
```

--- 
### ConsumerModule.csproj

```csharp
[Object]
public class ConsumerModule
{
    [Function]
    public async Task<string> UseInterfaceExample()
    {
        var example = Dag.InterfaceExample();
        var implementation = Dag.InterfaceImplementation();
        var converted = implementation.AsInterfaceExampleProcessor();
        return await example.ProcessText(converted, "Hello, Dagger!");
    }
}
```

---

**Key points:**
- Mark interfaces with `[Interface]` and methods with `[Function]`
- Accept interface parameters in your module functions
- Implementation modules don't need to explicitly declare they implement the interface
- Dagger uses **structural typing** - any module with matching method signatures is compatible

See the [interface example](./examples/interface-example/) and [interface tests](../../core/integration/testdata/modules/csharp/ifaces/) for complete examples.

---

## 📦 Client Library Usage

### Installation

Add the DaggerIO NuGet package to your project:

```bash
dotnet add package DaggerIO
```

### Example: Standalone Client

```csharp
using Dagger;

// Build a custom pipeline programmatically
var result = await Dag
    .Container()
    .From("alpine:latest")
    .WithExec(new[] { "echo", "Hello from Dagger!" })
    .Stdout();

Console.WriteLine(result);
```

### Running Your Client App

Use `dagger run` to ensure a Dagger session is available:

```bash
dagger run dotnet run
```

See the [standalone-client example](./examples/standalone-client/) for more detailed usage patterns.

---

## How It Works

### Module Runtime

When you run `dagger init --sdk=csharp`, the runtime generates everything needed:

1. SDK source code is generated into your module's `sdk/` directory
2. Template files are created (`Main.cs`, `Program.cs`, `.csproj`)
3. The Dagger engine executes your module via `dotnet run`

**Execution flow:**
```
Dagger Engine → dotnet run → ModuleRuntime → Your module functions
```

The `ModuleRuntime` (in the SDK) handles:
- Discovering classes marked with `[Object]`
- Discovering methods marked with `[Function]`
- Schema registration when called with `--register`
- Function invocation when called by the engine

### Generated Code

The Dagger client API is generated from the GraphQL schema at build time, providing:
- Type-safe access to all Dagger API types
- IntelliSense support with XML documentation
- Proper async/await patterns
- Fluent method chaining

Access the API through the `Dag` property:
```csharp
var container = Dag
    .Container()
    .From("alpine:latest")
    .WithExec(new[] { "echo", "hello" });
```

---

## Development

The SDK is managed with a Dagger module in `./dev`. To see available tasks:

```shell
dagger call -m dev
```

### Common Tasks

Run tests:

```shell
dagger call -m dev test --introspection-json=<path>
```

Check for linting violations:
```shell
dagger call -m dev lint
```

Re-format code:
```shell
dagger call -m dev format export --path=.
```

---

## Learn More

- [Documentation](https://docs.dagger.io/sdk/csharp)
- [Source code](https://github.com/dagger/dagger/tree/main/sdk/csharp)
- [Examples](./examples/)
