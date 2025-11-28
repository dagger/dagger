using System;
using Dagger;

/// <summary>
/// Example demonstrating constructor usage in Dagger modules.
/// Constructor parameters automatically become module configuration options.
/// </summary>
[Object]
public class ConstructorExample
{
    private readonly string _greeting;
    private readonly int _port;
    private readonly bool _enableLogging;

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
        bool enableLogging = false)
    {
        _greeting = greeting;
        _port = port;
        _enableLogging = enableLogging;
        
        if (_enableLogging)
        {
            Console.Error.WriteLine($"[ConstructorExample] Initialized with greeting='{_greeting}', port={_port}");
        }
    }

    /// <summary>
    /// Greets someone using the configured greeting.
    /// </summary>
    /// <param name="name">The name to greet</param>
    [Function]
    public string Greet(string name)
    {
        var message = $"{_greeting}, {name}!";
        if (_enableLogging)
        {
            Console.Error.WriteLine($"[ConstructorExample] Generated greeting: {message}");
        }
        return message;
    }

    /// <summary>
    /// Returns the configured port number.
    /// </summary>
    [Function]
    public int GetPort()
    {
        return _port;
    }

    /// <summary>
    /// Creates a service configuration string using constructor values.
    /// </summary>
    [Function]
    public string ServiceConfig()
    {
        return $"Service running on port {_port} with greeting '{_greeting}'";
    }
}
