# Standalone Client Example

This example demonstrates using the Dagger SDK as a **client library** in a regular .NET application (not as a Dagger module).

## Two Ways to Use the Dagger C# SDK

### 1. As a Dagger Module Runtime (Source-based)

When you run `dagger init --sdk=csharp`, you create a Dagger module where:

- The SDK source code is generated into your module's `sdk/` directory
- You write functions decorated with `[Function]`
- The Dagger engine loads and executes your module

### 2. As a Client Library (NuGet package)

You can also use Dagger.SDK as a regular NuGet package in any .NET application:

- Install via NuGet: `dotnet add package DaggerIO --prerelease`
- Use the `Dag` API directly in your code
- Build CI/CD pipelines programmatically

## Running This Example

### Prerequisites

- .NET 10.0 SDK
- Dagger CLI installed and running (`dagger version`)
- A Dagger session active (or use `dagger run`)

### Option 1: With dagger run (Recommended)

```bash
cd examples/standalone-client
dagger run dotnet run
```

This ensures a Dagger session is available via environment variables.

### Option 2: Manual session

```bash
# In one terminal, start a session
dagger session

# In another terminal
cd examples/standalone-client
dotnet run
```

## What This Example Shows

1. **Simple container execution** - Run commands in containers
2. **Multi-step pipelines** - Chain operations together
3. **Directory operations** - Create and mount directories
4. **Container customization** - Build custom container images
5. **Environment variables** - Pass configuration to containers

## Using Dagger.SDK in Your Own Project

### Install from NuGet

```bash
dotnet new console -n MyDaggerApp
cd MyDaggerApp
dotnet add package DaggerIO --prerelease
```

> **Note:** Currently published as `0.1.0-preview`. Use `--prerelease` flag to install preview versions.

### Write your pipeline

```csharp
using Dagger;

var result = await Dag
    .Container()
    .From("alpine:latest")
    .WithExec(new[] { "echo", "Hello!" })
    .Stdout();

Console.WriteLine(result);
```

### Run with Dagger

```bash
dagger run dotnet run
```

## Key Differences from Module Usage

| Aspect | Module (Runtime) | Client Library |
|--------|------------------|----------------|
| **Distribution** | Source generated into module | NuGet package (DaggerIO) |
| **Entry Point** | Dagger calls your functions | You call Dagger API |
| **Attributes** | Uses `[Object]`, `[Function]` | Not required |
| **Execution** | `dagger call my-function` | `dagger run dotnet run` |
| **Use Case** | Create reusable Dagger functions | Build custom CI/CD scripts |
| **Installation** | `dagger init --sdk=csharp` | `dotnet add package DaggerIO --prerelease` |

## Learn More

- [Dagger Documentation](https://docs.dagger.io)
- [C# SDK Architecture](../../ARCHITECTURE.md)
- [Dagger Quickstart](https://docs.dagger.io/quickstart)
