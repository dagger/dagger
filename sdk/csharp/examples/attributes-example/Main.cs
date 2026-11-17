using Dagger;

/// <summary>
/// Example demonstrating all Dagger attributes and their usage patterns.
/// This module showcases: [Object], [Function], [Name], [DefaultPath], [Deprecated], [Ignore], [Check], and enum attributes.
/// For the [Constructor] static-factory attribute, see factory-example.
/// </summary>
[Object]
public class AttributesExample
{
    // Constructor parameters must map to public properties for proper serialization:
    // constructor results serialize public properties, and later calls reconstruct
    // the object by re-running the constructor from parent state.
    public string Greeting { get; set; } = "Hello";
    public int MaxRetries { get; set; } = 3;

    /// <summary>
    /// Initializes a new instance of the AttributesExample class.
    /// Constructor parameters become module configuration (top-level flags on
    /// `dagger call`, or the `with(...)` field in raw queries).
    /// [Name] renames the registered argument; the registered name must match
    /// the camelCase name of the backing property so state round-trips.
    /// </summary>
    /// <param name="strGreeting">The greeting message to use</param>
    /// <param name="intMaxRetries">Maximum number of retries</param>
    public AttributesExample(
        [Name("greeting")] string strGreeting = "Hello",
        [Name("maxRetries")] int intMaxRetries = 3
    )
    {
        Greeting = strGreeting;
        MaxRetries = intMaxRetries;
        ConfigInfo = $"Config: {Greeting} / {MaxRetries}";
    }

    /// <summary>
    /// Demonstrates [Function(Name = "...")] to customize the exposed function name.
    /// This method is exposed as "greet" (kebab-case `greet` on the CLI) instead of "sayHello".
    /// Note: [Name] is parameter-only; methods and properties rename via [Function(Name = ...)].
    /// </summary>
    [Function(Name = "greet")]
    public string SayHello([Name("userName")] string name = "World")
    {
        return $"{Greeting}, {name}!";
    }

    /// <summary>
    /// Demonstrates [Function(Name)] on a property to customize the exposed field name.
    /// This property is exposed as "customMessage" in the API.
    /// Note: properties exposed as fields must have a setter (private is fine) for serialization.
    /// </summary>
    [Function(Name = "customMessage")]
    public string CustomMessage { get; set; } = "This is a custom message";

    /// <summary>
    /// Demonstrates a public property automatically exposed as a field.
    /// Auto-exposed properties must have a setter (private is fine) so object
    /// state can round-trip between calls; get-only computed properties
    /// (e.g. expression-bodied `=>`) cannot be rehydrated. This one is
    /// computed once in the constructor.
    /// </summary>
    public string ConfigInfo { get; private set; }

    /// <summary>
    /// Demonstrates [DefaultPath] attribute for Directory parameters.
    /// If no directory is provided, it defaults to current directory (".").
    /// Also demonstrates [Ignore] to exclude certain patterns from the directory.
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
    /// Demonstrates running a container as a check.
    /// Checks return void or Task: awaiting Sync() throws on a non-zero exit,
    /// which fails the check.
    /// </summary>
    [Function]
    [Check]
    public async Task ValidateWithContainer()
    {
        await Dag.Container()
            .From("alpine:3")
            .WithExec(["sh", "-c", "echo 'Running container check' && exit 0"])
            .Sync();
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
