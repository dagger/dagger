namespace Dagger;

/// <summary>
/// Marks an enum type as a Dagger enum.
/// </summary>
[AttributeUsage(AttributeTargets.Enum, AllowMultiple = false, Inherited = false)]
public class EnumAttribute : Attribute
{
    /// <summary>
    /// The name of the enum. If not provided, the enum type name will be used.
    /// </summary>
    public string? Name { get; set; }

    /// <summary>
    /// The description of the enum.
    /// </summary>
    public string? Description { get; set; }
}
