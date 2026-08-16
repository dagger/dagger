namespace Dagger;

/// <summary>
/// Marks a C# interface as a Dagger interface type.
/// Interfaces define contracts that objects can implement, enabling polymorphism across modules.
/// </summary>
[AttributeUsage(AttributeTargets.Interface, AllowMultiple = false, Inherited = false)]
public class InterfaceAttribute : Attribute
{
    /// <summary>
    /// Optional custom name for the interface in the Dagger API.
    /// If not specified, the interface name is used.
    /// </summary>
    public string? Name { get; set; }

    /// <summary>
    /// Optional description of the interface.
    /// </summary>
    public string? Description { get; set; }
}
