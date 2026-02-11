using System.Collections.Immutable;

namespace Dagger.GraphQL;

/// <summary>
/// Represents a GraphQL field with a name and arguments.
/// </summary>
/// <param name="name">The field name.</param>
/// <param name="args">The field arguments.</param>
public class Field(string name, ImmutableList<Argument> args)
{
    /// <summary>
    /// The field name.
    /// </summary>
    public string Name { get; } = name;

    /// <summary>
    /// The field arguments.
    /// </summary>
    public ImmutableList<Argument> Args { get; } = args;
}
