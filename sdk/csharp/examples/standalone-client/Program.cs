using Dagger;
using static Dagger.Client;

// Example 1: Simple container execution
Console.WriteLine("Example 1: Running a simple command");
var output = await Dag.Container()
    .From("alpine:latest")
    .WithExec(["echo", "Hello from Dagger!"])
    .Stdout();
Console.WriteLine(output);

// Example 2: Run tests in a container
Console.WriteLine("\nExample 2: Running tests in a container");
var exitCode = await Dag.Container()
    .From("mcr.microsoft.com/dotnet/sdk:10.0")
    .WithDirectory("/src", Dag.Host().Directory("."))
    .WithWorkdir("/src")
    .WithExec(["dotnet", "build"])
    .ExitCode();

Console.WriteLine($"Build exit code: {exitCode}");

// Example 3: Build and package
Console.WriteLine("\nExample 3: Building application");
var source = Dag.Host().Directory(".");

var buildOutput = Dag.Container()
    .From("mcr.microsoft.com/dotnet/sdk:10.0")
    .WithDirectory("/src", source)
    .WithWorkdir("/src")
    .WithExec(["dotnet", "publish", "-c", "Release", "-o", "/app"])
    .Directory("/app");

var files = await buildOutput.Entries();
Console.WriteLine($"Published {files.Length} files");

Console.WriteLine("\n✅ All examples completed!");
