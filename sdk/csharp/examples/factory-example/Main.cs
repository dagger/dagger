using Dagger;

/// <summary>
/// A Dagger module demonstrating the [Constructor] attribute for async factory initialization.
///
/// The [Constructor] attribute marks a static async method as the factory method for creating
/// module instances. This is useful when initialization requires async operations like
/// loading configuration, connecting to services, or preparing resources.
/// </summary>
[Object]
public class FactoryExample
{
    /// <summary>
    /// The initialization message set during factory creation
    /// </summary>
    [Function]
    public string Message { get; private set; }

    private DateTime _createdAt;

    private FactoryExample(string message, DateTime createdAt)
    {
        Message = message;
        _createdAt = createdAt;
    }

    /// <summary>
    /// Factory method that performs async initialization.
    /// The [Constructor] attribute tells Dagger to use this instead of the default constructor.
    /// </summary>
    [Constructor]
    public static async Task<FactoryExample> CreateAsync(string message = "Hello from factory!")
    {
        // Simulate async initialization work (e.g., loading config, connecting to services)
        await Task.Delay(100);

        var createdAt = DateTime.UtcNow;
        return new FactoryExample(message, createdAt);
    }

    /// <summary>
    /// Returns the initialization message and timestamp
    /// </summary>
    [Function]
    public string GetInfo()
    {
        return $"{Message} (created at {_createdAt:u})";
    }
}
