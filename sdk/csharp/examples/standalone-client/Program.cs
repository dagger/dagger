using static Dagger.Client;

// Check if running within a Dagger session
if (
    string.IsNullOrEmpty(Environment.GetEnvironmentVariable("DAGGER_SESSION_PORT"))
    && string.IsNullOrEmpty(Environment.GetEnvironmentVariable("DAGGER_SESSION_TOKEN"))
)
{
    Console.WriteLine("❌ ERROR: Not running in a Dagger session!");
    Console.WriteLine();
    Console.WriteLine("This example requires a Dagger session to be active.");
    Console.WriteLine("Please run this application using:");
    Console.WriteLine();
    Console.WriteLine("  dagger run dotnet run");
    Console.WriteLine();
    Console.WriteLine("Or start a Dagger session in another terminal:");
    Console.WriteLine("  dagger session");
    Environment.Exit(1);
}

Console.WriteLine("✅ Running in Dagger session\n");

// Example 1: Simple container execution
Console.WriteLine("Example 1: Running a simple command in a container");
var output = await Dag.Container()
    .From("alpine:latest")
    .WithExec(["echo", "Hello from Dagger!"])
    .StdoutAsync();
Console.WriteLine($"Output: {output}");

// Example 2: Host standalone-client build in container
Console.WriteLine("\nExample 2: Building current directory in a .NET container");
var src = Dag.Host().Directory(".");
output = await Dag.Container()
    .From("mcr.microsoft.com/dotnet/sdk:10.0")
    .WithDirectory("/src", src)
    .WithWorkdir("/src")
    .WithExec(new[] { "dotnet", "build", "-c", "Release" })
    .WithExec(new[] { "dotnet", "publish", "-c", "Release", "-o", "/app" })
    .StdoutAsync();
Console.WriteLine($"Build Output: {output}");


Console.WriteLine("\n✅ All examples completed successfully!");
