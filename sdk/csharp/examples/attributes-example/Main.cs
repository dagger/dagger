using Dagger;

/// <summary>
/// Example demonstrating all Dagger attributes and their usage patterns.
/// This module showcases: [Object], [Function], [Name], [DefaultPath], [Deprecated], [Ignore], [Check], [Constructor], and enum attributes.
/// </summary>
[Object]
public class AttributesExample
{
    // Constructor parameters must map to public properties for proper serialization.
    // Dagger uses JSON serialization which only preserves public properties, not private fields.
    public string Greeting { get; set; } = "Hello";
    public int MaxRetries { get; set; } = 3;

    /// <summary>
    /// Initializes a new instance of the AttributesExample class.
    /// Constructor parameters become module configuration.
    /// </summary>
    /// <param name="strGreeting">The greeting message to use</param>
    /// <param name="intMaxRetries">Maximum number of retries</param>
    public AttributesExample(
        [Name("greeting")] string strGreeting = "Hello",
        [Name("max-retries")] int intMaxRetries = 3
    )
    {
        Greeting = strGreeting;
        MaxRetries = intMaxRetries;
        // ConfigValue will be computed dynamically via getter
    }

    /// <summary>
    /// Demonstrates [Constructor] attribute for alternative static factory method.
    /// This allows async initialization and factory patterns.
    /// </summary>
    [Constructor]
    public static async Task<AttributesExample> FromConfig(string configPath = "config.json")
    {
        // In real scenario, load config asynchronously
        await Task.Delay(1);
        return new AttributesExample("Hello from config", 5);
    }

    /// <summary>
    /// Demonstrates [Function(Name = "...")] to customize the GraphQL function name.
    /// This function is exposed as "say-hello" instead of "SayHello".
    /// Note: [Name] attribute does NOT work on methods, only on parameters and properties.
    /// </summary>
    [Function(Name = "say-hello-override")]
    public string SayHello([Name("user-name-override")] string userName = "World")
    {
        return $"{Greeting}, {userName}!";
    }

    /// <summary>
    /// Demonstrates [Function(Name)] on property to customize field name.
    /// This property is exposed as "custom-message" in the API.
    /// Note: Properties with [Function] must have a setter for Dagger serialization.
    /// </summary>
    [Function(Name = "custom-message")]
    public string CustomMessage { get; set; } = "This is a custom message";

    /// <summary>
    /// Demonstrates computed property that's automatically exposed.
    /// Computes the value dynamically based on current Greeting and MaxRetries.
    /// </summary>
    public string ConfigInfo => $"Config: {Greeting} / {MaxRetries}";

    /// <summary>
    /// Demonstrates [DefaultPath] attribute for Directory parameters.
    /// If no directory is provided, it defaults to current directory (".").
    /// Also demonstrates [Ignore] to exclude certain patterns from the directory.
    /// NOTE: Currently throws InvalidOperationException due to SDK bug in EntriesAsync().
    /// The error occurs during deserialization: expects Array but gets Object.
    /// </summary>
    [Function]
    public async Task<string> AnalyzeSource(
        [DefaultPath(".")] [Ignore("node_modules", ".git", "**/*.log")] Directory source
    )
    {
        var entries = await source.Entries();
        return $"Found {entries.Length} entries in source directory (excluding ignored patterns)";
    }

    /// <summary>
    /// Demonstrates [Deprecated] attribute on a parameter to mark it as deprecated.
    /// </summary>
    [Function]
    public string ProcessData(
        [Deprecated("Use newInput instead")] string oldInput = "",
        string newInput = "default"
    )
    {
        return $"Processed: {(string.IsNullOrEmpty(oldInput) ? newInput : oldInput)}";
    }

    /// <summary>
    /// Demonstrates using an enum with [Enum] and [EnumValue] attributes.
    /// </summary>
    [Function]
    public string ProcessWithMode(ProcessMode mode, string input)
    {
        return mode switch
        {
            ProcessMode.Fast => $"Fast processing: {input}",
            ProcessMode.Thorough => $"Thorough processing: {input}",
            ProcessMode.Verbose => $"Verbose processing: {input} (max retries: {MaxRetries})",
            _ => throw new ArgumentOutOfRangeException(nameof(mode)),
        };
    }

    /// <summary>
    /// Demonstrates [Check] attribute for validation functions.
    /// Check functions validate module state and throw on failure.
    /// </summary>
    [Function]
    [Check]
    public void ValidateConfiguration()
    {
        if (MaxRetries < 1)
        {
            throw new InvalidOperationException("Max retries must be at least 1");
        }
    }

    /// <summary>
    /// Demonstrates async check with [DefaultPath] to make parameters contextually optional.
    /// Check functions cannot have required parameters.
    /// </summary>
    [Function]
    [Check]
    public async Task ValidateTests([DefaultPath(".")] Directory source)
    {
        var entries = await source.Entries();
        if (entries.Length == 0)
        {
            throw new InvalidOperationException("Source directory is empty");
        }
    }

    /// <summary>
    /// Demonstrates container-based check that returns a Container.
    /// The check passes if the container exits with code 0.
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
    /// Returns configuration info demonstrating constructor-injected values.
    /// </summary>
    [Function]
    public string GetConfig()
    {
        return $"Greeting: '{Greeting}', Max Retries: {MaxRetries}";
    }
}

/// <summary>
/// Demonstrates [Enum] attribute for custom enum types.
/// Each value can use [EnumValue] to provide description or deprecation metadata.
/// </summary>
[Enum(Description = "Demonstrates [Enum] attribute for custom enum types.")]
public enum ProcessMode
{
    /// <summary>
    /// Fast processing mode with minimal validation.
    /// </summary>
    [EnumValue(Description = "Fast processing mode with minimal validation")]
    Fast,

    /// <summary>
    /// Thorough processing mode with full validation.
    /// </summary>
    [EnumValue(Description = "Thorough processing mode with full validation")]
    Thorough,

    /// <summary>
    /// Verbose processing mode with detailed logging.
    /// </summary>
    [EnumValue(Description = "Verbose processing mode with detailed logging")]
    Verbose,
}
