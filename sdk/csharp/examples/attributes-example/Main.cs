using Dagger;

/// <summary>
/// Example demonstrating Dagger attributes like [DefaultPath] and [Ignore].
/// These attributes customize how Directory parameters are loaded and filtered.
/// </summary>
[Object]
public class AttributesExample
{
    /// <summary>
    /// Demonstrates [DefaultPath] attribute.
    /// If no directory is provided via CLI, it defaults to current directory (".").
    /// </summary>
    /// <param name="source">Source directory to analyze</param>
    [Function]
    public async Task<string> AnalyzeSource([DefaultPath(".")] Directory source)
    {
        var id = await source.IdAsync();
        return $"Analyzed source directory (ID: {id})";
    }

    /// <summary>
    /// Demonstrates [DefaultPath] with subdirectory.
    /// Will auto-load from 'src' subdirectory if it exists.
    /// </summary>
    /// <param name="srcDir">Source directory</param>
    [Function]
    public async Task<string> AnalyzeSrcFolder([DefaultPath("./src")] Directory srcDir)
    {
        var id = await srcDir.IdAsync();
        return $"Analyzed src folder (ID: {id})";
    }

    /// <summary>
    /// Demonstrates working with Directory without Ignore attribute.
    /// Note: [Ignore] attribute has known issues in current SDK version.
    /// </summary>
    [Function]
    public async Task<string> GetDirectoryInfo([DefaultPath(".")] Directory dir)
    {
        var id = await dir.IdAsync();
        return $"Directory loaded successfully (ID: {id})";
    }

    /// <summary>
    /// Basic check function that validates a simple condition.
    /// Check functions are used to validate module state and return void on success or throw on failure.
    /// Throws an exception if validation fails.
    /// </summary>
    [Function]
    [Check]
    public void ValidateFormat()
    {
        // Example check: validate formatting
        var needsFormatting = false; // In real scenario, check actual files
        if (needsFormatting)
        {
            throw new InvalidOperationException("Code formatting validation failed");
        }
    }

    /// <summary>
    /// Check function that validates linting rules.
    /// </summary>
    [Function]
    [Check]
    public void Lint()
    {
        // Example check: validate linting
        var hasLintErrors = false; // In real scenario, run linter
        if (hasLintErrors)
        {
            throw new InvalidOperationException("Linting validation failed");
        }
    }

    /// <summary>
    /// Check function with async/Task return type.
    /// Validates test execution.
    /// Note: Uses [DefaultPath] to make the parameter contextually optional,
    /// as check functions cannot have required parameters.
    /// </summary>
    [Function]
    [Check]
    public async Task ValidateTests([DefaultPath(".")] Directory source)
    {
        var result = await Dag.Container()
            .From("mcr.microsoft.com/dotnet/sdk:8.0")
            .WithDirectory("/src", source)
            .WithWorkdir("/src")
            .WithExec(["dotnet", "test", "--no-build"])
            .SyncAsync();

        // Check passes if we reach here without exception
    }

    /// <summary>
    /// Container-based check that returns a container to execute.
    /// The check passes if the container exits with code 0, fails otherwise.
    /// This pattern matches Go SDK's container-based checks.
    /// </summary>
    [Function]
    [Check]
    public Container ValidateWithContainer()
    {
        return Dag.Container()
            .From("alpine:3")
            .WithExec(["sh", "-c", "echo 'Running container check' && exit 0"]);
    }

    /// <summary>
    /// Async container-based check with Task&lt;Container&gt; return type.
    /// Demonstrates building a container asynchronously before returning for execution.
    /// </summary>
    [Function]
    [Check]
    public async Task<Container> ValidateWithAsyncContainer([DefaultPath(".")] Directory source)
    {
        // Build container asynchronously
        var container = await Task.FromResult(
            Dag.Container()
                .From("alpine:3")
                .WithDirectory("/workspace", source)
                .WithWorkdir("/workspace")
                .WithExec(["sh", "-c", "test -d /workspace && exit 0 || exit 1"])
        );

        return container;
    }
}

