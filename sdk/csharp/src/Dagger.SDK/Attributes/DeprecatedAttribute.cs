using System;

namespace Dagger;

/// <summary>
/// Marks a function parameter as deprecated with an optional message explaining the deprecation.
/// </summary>
/// <example>
/// <code>
/// [Function]
/// public string Process(
///     [Deprecated("Use newParam instead")] string oldParam,
///     string newParam)
/// {
///     return newParam;
/// }
/// </code>
/// </example>
[AttributeUsage(AttributeTargets.Parameter, AllowMultiple = false, Inherited = false)]
public class DeprecatedAttribute : Attribute
{
    /// <summary>
    /// Gets the deprecation message.
    /// </summary>
    public string Message { get; }

    /// <summary>
    /// Initializes a new instance of the <see cref="DeprecatedAttribute"/> class.
    /// </summary>
    /// <param name="message">The deprecation message explaining why the parameter is deprecated and what to use instead.</param>
    public DeprecatedAttribute(string message)
    {
        Message = message;
    }
}
