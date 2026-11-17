using System;

namespace Dagger;

/// <summary>
/// Loads a <see cref="Container"/> argument from the given image address when the
/// caller omits it. Only valid on <c>Container</c> parameters.
/// </summary>
/// <example>
/// <code>
/// [Function]
/// public Container Build([DefaultAddress("alpine:latest")] Container ctr)
/// {
///     return ctr.WithExec(["echo", "hello"]);
/// }
/// </code>
/// </example>
[AttributeUsage(AttributeTargets.Parameter, AllowMultiple = false, Inherited = false)]
public class DefaultAddressAttribute : Attribute
{
    /// <summary>
    /// Gets the container address to load when the argument is omitted.
    /// </summary>
    public string Address { get; }

    /// <summary>
    /// Initializes a new instance of the <see cref="DefaultAddressAttribute"/> class.
    /// </summary>
    /// <param name="address">The container address, for example "alpine:latest".</param>
    public DefaultAddressAttribute(string address)
    {
        Address = address ?? throw new ArgumentNullException(nameof(address));
    }
}
