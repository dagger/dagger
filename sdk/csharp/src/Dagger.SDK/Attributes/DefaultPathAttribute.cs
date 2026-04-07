using System;

namespace Dagger;

/// <summary>
/// Specifies the default path to use when loading a Directory or File parameter.
/// This attribute is only valid on parameters of type Directory or File.
/// </summary>
/// <example>
/// <code>
/// [Function]
/// public async Task&lt;string&gt; Build(
///     [DefaultPath(".")] Directory source)
/// {
///     return await source.Entries().GetAsync();
/// }
/// </code>
/// </example>
[AttributeUsage(AttributeTargets.Parameter, AllowMultiple = false, Inherited = false)]
public class DefaultPathAttribute : Attribute
{
    /// <summary>
    /// Gets the default path to use for loading the directory or file.
    /// </summary>
    public string Path { get; }

    /// <summary>
    /// Initializes a new instance of the <see cref="DefaultPathAttribute"/> class.
    /// </summary>
    /// <param name="path">The default path to use for loading the directory or file.</param>
    public DefaultPathAttribute(string path)
    {
        Path = path ?? throw new ArgumentNullException(nameof(path));
    }
}
