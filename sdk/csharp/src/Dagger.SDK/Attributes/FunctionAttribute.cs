namespace Dagger;

/// <summary>
/// Marks a method as a Dagger function.
/// </summary>
[AttributeUsage(AttributeTargets.Method, AllowMultiple = false, Inherited = false)]
public class FunctionAttribute : Attribute
{
    /// <summary>
    /// Gets or sets the name of the function.
    /// </summary>
    public string? Name { get; set; }

    /// <summary>
    /// Gets or sets the description of the function.
    /// </summary>
    public string? Description { get; set; }

    /// <summary>
    /// Gets or sets the cache policy for the function. Can be "never", "session", or a duration string like "5m", "1h".
    /// </summary>
    public string? Cache { get; set; }

    /// <summary>
    /// Gets or sets the deprecation message if the function is deprecated.
    /// </summary>
    public string? Deprecated { get; set; }
}
