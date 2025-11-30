namespace Dagger;

/// <summary>
/// Marks a property or field as exposed in a Dagger object.
/// </summary>
[AttributeUsage(AttributeTargets.Property | AttributeTargets.Field, AllowMultiple = false, Inherited = false)]
public class FieldAttribute : Attribute
{
    /// <summary>
    /// The name of the field. If not provided, the property/field name will be used.
    /// </summary>
    public string? Name { get; set; }

    /// <summary>
    /// The description of the field.
    /// </summary>
    public string? Description { get; set; }

    /// <summary>
    /// Deprecation message if the field is deprecated.
    /// </summary>
    public string? Deprecated { get; set; }
}
