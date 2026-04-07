# Standalone Client Example

This example demonstrates using the Dagger .NET SDK as a **NuGet package** in a regular console application.

## Installation

```bash
dotnet add package DaggerIO
```

## Running This Example

```bash
dagger run dotnet run
```

## What This Shows

- Running commands in containers
- Building .NET applications in containers
- Working with directories and files

## Create Your Own

```bash
dotnet new console -n MyDaggerApp
cd MyDaggerApp
dotnet add package DaggerIO
```

Edit `Program.cs`:

```csharp
using Dagger;

var output = await Dag
    .Container()
    .From("alpine:latest")
    .WithExec(new[] { "echo", "Hello from Dagger!" })
    .StdoutAsync();

Console.WriteLine(output);
```

Run it:

```bash
dagger run dotnet run
```

## Learn More

- [Documentation](https://docs.dagger.io/sdk/csharp)
- [API Reference](https://docs.dagger.io/api/reference)
