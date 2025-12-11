namespace Dagger;

/// <summary>
/// Provides additional metadata for enum values.
/// </summary>
[AttributeUsage(AttributeTargets.Field, AllowMultiple = false, Inherited = false)]
public class EnumValueAttribute : Attribute
{
    /// <summary>
    /// The description of the enum value.
    /// </summary>
    public string? Description { get; set; }

    /// <summary>
    /// Deprecation message if the enum value is deprecated.
    /// </summary>
    public string? Deprecated { get; set; }
}
