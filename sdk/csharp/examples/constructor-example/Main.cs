using Dagger;

/// <summary>
/// Example demonstrating constructor usage in Dagger modules.
/// Constructor parameters automatically become module configuration options.
/// Note: Constructor parameters must map to public properties for proper serialization.
/// </summary>
[Object]
public class ConstructorExample
{
    public string Greeting { get; set; } = "Hello";
    public int Port { get; set; } = 8080;
    public bool EnableLogging { get; set; } = false;

    /// <summary>
    /// Creates a new ConstructorExample instance.
    /// Constructor parameters are exposed as module arguments when calling functions.
    /// </summary>
    /// <param name="greeting">The greeting message to use</param>
    /// <param name="port">The port number for the service</param>
    /// <param name="enableLogging">Enable logging output</param>
    public ConstructorExample(
        string greeting = "Hello",
        int port = 8080,
        bool enableLogging = false
    )
    {
        Greeting = greeting;
        Port = port;
        EnableLogging = enableLogging;

        if (EnableLogging)
        {
            Console.Error.WriteLine(
                $"[ConstructorExample] Initialized with greeting='{Greeting}', port={Port}"
            );
        }
    }

    /// <summary>
    /// Greets someone using the configured greeting.
    /// </summary>
    /// <param name="name">The name to greet</param>
    [Function]
    public string Greet(string name)
    {
        var message = $"{Greeting}, {name}!";
        if (EnableLogging)
        {
            Console.Error.WriteLine($"[ConstructorExample] Generated greeting: {message}");
        }
        return message;
    }

    /// <summary>
    /// Returns the configured port number.
    /// </summary>
    [Function]
    public int GetPort() => Port;

    /// <summary>
    /// Creates a service configuration string using constructor values.
    /// </summary>
    [Function]
    public string ServiceConfig() => $"Service running on port {Port} with greeting '{Greeting}'";
}
