using System;
using System.Collections.Generic;
using System.Linq;

namespace Dagger;

/// <summary>
/// Specifies file patterns to ignore when loading a Directory parameter.
/// This attribute is only valid on parameters of type Directory.
/// Patterns use the doublestar glob format (e.g., "**/*.log", "node_modules").
/// </summary>
/// <example>
/// <code>
/// [DaggerFunction]
/// public async Task&lt;string&gt; Build(
///     [DefaultPath(".")]
///     [Ignore("node_modules", ".git", "**/*.log")]
///     Directory source)
/// {
///     return await source.Entries().GetAsync();
/// }
/// </code>
/// </example>
[AttributeUsage(AttributeTargets.Parameter, AllowMultiple = false, Inherited = false)]
public class IgnoreAttribute : Attribute
{
    /// <summary>
    /// Gets the collection of file patterns to ignore.
    /// Patterns use the doublestar glob format.
    /// </summary>
    public IReadOnlyList<string> Patterns { get; }

    /// <summary>
    /// Initializes a new instance of the <see cref="IgnoreAttribute"/> class.
    /// </summary>
    /// <param name="patterns">File patterns to ignore using doublestar glob format (e.g., "**/*.log", "node_modules").</param>
    public IgnoreAttribute(params string[] patterns)
    {
        if (patterns == null || patterns.Length == 0)
        {
            throw new ArgumentException(
                "At least one ignore pattern must be specified.",
                nameof(patterns)
            );
        }

        Patterns = patterns.Where(p => !string.IsNullOrWhiteSpace(p)).ToList().AsReadOnly();

        if (Patterns.Count == 0)
        {
            throw new ArgumentException(
                "At least one non-empty ignore pattern must be specified.",
                nameof(patterns)
            );
        }
    }
}
