using Dagger;

/// <summary>
/// Demonstrates default parameter values in Dagger functions.
/// Shows how to use default values for primitives, strings, enums, and nullable types.
/// </summary>
[Object]
public class DefaultsExample
{
    /// <summary>
    /// Log levels for the logging example
    /// </summary>
    [Enum]
    public enum LogLevel
    {
        Debug,
        Info,
        Warning,
        Error
    }

    /// <summary>
    /// Compression levels for the compression example
    /// </summary>
    [Enum]
    public enum CompressionLevel
    {
        None,
        Fast,
        Best
    }

    /// <summary>
    /// Creates a file with optional custom content (defaults to "data.txt" with "Hello, Dagger!")
    /// </summary>
    [Function]
    public Directory CreateFile(
        string filename = "data.txt",
        string content = "Hello, Dagger!")
    {
        return Dag.Directory().WithNewFile(filename, content);
    }

    /// <summary>
    /// Logs a message with a specified log level (defaults to Info)
    /// </summary>
    [Function]
    public string Log(
        string message,
        LogLevel level = LogLevel.Info)
    {
        return $"[{level}] {message}";
    }

    /// <summary>
    /// Compresses a directory with optional compression level (defaults to Fast)
    /// </summary>
    [Function]
    public async Task<string> Compress(
        [DefaultPath(".")] Directory source,
        CompressionLevel level = CompressionLevel.Fast)
    {
        return $"Compressing directory (ID: {await source.IdAsync()}) with level: {level}";
    }

    /// <summary>
    /// Creates a container with optional image and command
    /// </summary>
    [Function]
    public Container CreateContainer(
        string image = "alpine:latest",
        string[]? command = null,
        int? port = null)
    {
        var ctr = Dag.Container().From(image);
        
        if (command != null && command.Length > 0)
        {
            ctr = ctr.WithExec(command);
        }
        
        if (port.HasValue)
        {
            ctr = ctr.WithExposedPort(port.Value);
        }
        
        return ctr;
    }

    /// <summary>
    /// Analyzes a directory with optional filters
    /// </summary>
    [Function]
    public async Task<string> AnalyzeDirectory(
        [DefaultPath(".")] Directory source,
        string pattern = "**/*",
        bool includeHidden = false,
        int? maxDepth = null)
    {
        var info = $"Analyzing directory (ID: {await source.IdAsync()})\n";
        info += $"  Pattern: {pattern}\n";
        info += $"  Include hidden: {includeHidden}\n";
        info += $"  Max depth: {(maxDepth.HasValue ? maxDepth.Value.ToString() : "unlimited")}";
        return info;
    }

    /// <summary>
    /// Demonstrates all types of default parameters in one function
    /// </summary>
    [Function]
    public string DemoAllDefaults(
        string name = "World",
        int count = 1,
        bool verbose = false,
        LogLevel level = LogLevel.Info,
        string[]? tags = null,
        double? ratio = null)
    {
        var result = $"Name: {name}\n";
        result += $"Count: {count}\n";
        result += $"Verbose: {verbose}\n";
        result += $"Level: {level}\n";
        result += $"Tags: {(tags != null ? string.Join(", ", tags) : "none")}\n";
        result += $"Ratio: {(ratio.HasValue ? ratio.Value.ToString() : "not set")}";
        return result;
    }
}