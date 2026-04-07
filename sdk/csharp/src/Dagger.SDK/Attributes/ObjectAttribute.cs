namespace Dagger;

/// <summary>
/// Marks a class as a Dagger module object.
/// </summary>
[AttributeUsage(AttributeTargets.Class, AllowMultiple = false, Inherited = false)]
public class ObjectAttribute : Attribute
{
    /// <summary>
    /// Gets or sets the name of the object.
    /// </summary>
    public string? Name { get; set; }

    /// <summary>
    /// Gets or sets the description of the object.
    /// </summary>
    public string? Description { get; set; }

    /// <summary>
    /// Gets or sets the deprecation message if the object is deprecated.
    /// </summary>
    public string? Deprecated { get; set; }
}
