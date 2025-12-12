# Dagger SDK for .NET

[![NuGet](https://img.shields.io/nuget/v/DaggerIO.svg)](https://www.nuget.org/packages/DaggerIO/)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://github.com/dagger/dagger/blob/main/LICENSE)

A client package for running [Dagger](https://dagger.io/) pipelines in .NET.

## What is the Dagger .NET SDK?

The Dagger .NET SDK contains everything you need to develop CI/CD pipelines in C#, and run them on any OCI-compatible container runtime.

## Requirements

- .NET 10.0 or later
- [Docker](https://docs.docker.com/engine/install/), or another OCI-compatible container runtime

A compatible version of the [Dagger CLI](https://docs.dagger.io/cli) is automatically downloaded and run by the SDK for you.

## Installation

```bash
dotnet add package DaggerIO
```

## Example

Create a `Program.cs` file:

```csharp
using Dagger;

var output = await Dag
    .Container()
    .From("alpine:latest")
    .WithExec(new[] { "echo", "Hello from Dagger!" })
    .StdoutAsync();

Console.WriteLine(output);
```

Run with:

```bash
dagger run dotnet run
```

Output:

```
Hello from Dagger!
```

> **Note:** It may take a while for it to finish, especially on first run with cold cache.

## More Examples

### Run Tests in a Container

```csharp
using Dagger;

var exitCode = await Dag
    .Container()
    .From("mcr.microsoft.com/dotnet/sdk:8.0")
    .WithDirectory("/src", Dag.Host().Directory("."))
    .WithWorkdir("/src")
    .WithExec(new[] { "dotnet", "test" })
    .ExitCodeAsync();

Environment.Exit(exitCode);
```

### Build and Publish a Container

```csharp
using Dagger;

var source = Dag.Host().Directory(".");

// Build
var buildOutput = Dag
    .Container()
    .From("mcr.microsoft.com/dotnet/sdk:8.0")
    .WithDirectory("/src", source)
    .WithWorkdir("/src")
    .WithExec(new[] { "dotnet", "publish", "-c", "Release", "-o", "/app" })
    .Directory("/app");

// Create runtime image
var image = Dag
    .Container()
    .From("mcr.microsoft.com/dotnet/runtime:8.0")
    .WithDirectory("/app", buildOutput)
    .WithEntrypoint(new[] { "dotnet", "/app/MyApp.dll" });

// Publish
var ref = await image.Publish("myregistry.io/myapp:latest");
Console.WriteLine($"Published: {ref}");
```

## Learn More

- [Documentation](https://docs.dagger.io/sdk/csharp)
- [API Reference](https://docs.dagger.io/api/reference)
- [Examples](https://github.com/dagger/dagger/tree/main/sdk/csharp/examples)
- [Source Code](https://github.com/dagger/dagger/tree/main/sdk/csharp)

## Creating Reusable Modules

Want to create reusable Dagger modules that others can call? See the [module development guide](https://docs.dagger.io/quickstart).

## License

Apache 2.0 - See [LICENSE](https://github.com/dagger/dagger/blob/main/LICENSE) for details.

