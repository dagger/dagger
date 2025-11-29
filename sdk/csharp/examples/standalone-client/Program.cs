using Dagger;

// Example 1: Simple container execution
Console.WriteLine("Example 1: Running a simple command");
var output = await Dag
    .Container()
    .From("alpine:latest")
    .WithExec(new[] { "echo", "Hello from Dagger!" })
    .StdoutAsync();
Console.WriteLine(output);

// Example 2: Run tests in a container
Console.WriteLine("\nExample 2: Running tests in a container");
var exitCode = await Dag
    .Container()
    .From("mcr.microsoft.com/dotnet/sdk:8.0")
    .WithDirectory("/src", Dag.Host().Directory("."))
    .WithWorkdir("/src")
    .WithExec(new[] { "dotnet", "build" })
    .ExitCodeAsync();

Console.WriteLine($"Build exit code: {exitCode}");

// Example 3: Build and package
Console.WriteLine("\nExample 3: Building application");
var source = Dag.Host().Directory(".");

var buildOutput = Dag
    .Container()
    .From("mcr.microsoft.com/dotnet/sdk:8.0")
    .WithDirectory("/src", source)
    .WithWorkdir("/src")
    .WithExec(new[] { "dotnet", "publish", "-c", "Release", "-o", "/app" })
    .Directory("/app");

var files = await buildOutput.EntriesAsync();
Console.WriteLine($"Published {files.Length} files");

Console.WriteLine("\n✅ All examples completed!");
