# Standalone Client Example

This example demonstrates using the Dagger .NET SDK as a **client library** in a regular console application (no module, no attributes).

The project consumes the published NuGet package, exactly as your own
application would. Package versions track the Dagger engine version they were
generated against (engine `v0.21.7` → `0.21.7-preview`):

```bash
dotnet add package DaggerIO --version 0.21.7-preview.1
```

(For in-repo development against unreleased SDK changes, swap the
`PackageReference` for
`<ProjectReference Include="..\..\src\Dagger.SDK\Dagger.SDK.csproj" />`.)

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
using static Dagger.Client;

var output = await Dag.Container()
    .From("alpine:latest")
    .WithExec(["echo", "Hello from Dagger!"])
    .Stdout();

Console.WriteLine(output);
```

Run it:

```bash
dagger run dotnet run
```

## Learn More

- [Documentation](https://docs.dagger.io/sdk/csharp)
- [API Reference](https://docs.dagger.io/api/reference)
