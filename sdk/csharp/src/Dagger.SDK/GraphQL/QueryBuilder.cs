using System.Collections.Immutable;
using System.Text;

namespace Dagger.GraphQL;

/// <summary>
/// A builder for constructing GraphQL query.
/// </summary>
public class QueryBuilder(ImmutableList<Field> path)
{
    /// <summary>
    /// The path of fields in the query.
    /// </summary>
    public readonly ImmutableList<Field> Path = path;

    private QueryBuilder()
        : this(ImmutableList<Field>.Empty) { }

    /// <summary>
    /// Select a field with name.
    /// </summary>
    /// <param name="name">The field name.</param>
    /// <returns>A new QueryBuilder instance.</returns>
    public QueryBuilder Select(string name)
    {
        return Select(name, ImmutableList<Argument>.Empty);
    }

    /// <summary>
    /// Select a field with name plus arguments.
    /// </summary>
    /// <param name="name">The field name.</param>
    /// <param name="args">The field arguments.</param>
    /// <returns>A new QueryBuilder instance.</returns>
    public QueryBuilder Select(string name, ImmutableList<Argument> args)
    {
        return Select(new Field(name, args));
    }

    /// <summary>
    /// Select a field.
    /// </summary>
    /// <param name="field">The field to select.</param>
    /// <returns>A new QueryBuilder instance.</returns>
    public QueryBuilder Select(Field field)
    {
        return new QueryBuilder(Path.Add(field));
    }

    /// <summary>
    /// Build GraphQL query.
    /// </summary>
    /// <returns>GraphQL query string</returns>
    public string Build()
    {
        var builder = new StringBuilder();
        builder.Append("query");
        foreach (var selection in Path)
        {
            builder.Append('{');
            builder.Append(selection.Name);
            if (selection.Args.Count > 0)
            {
                builder.Append('(');
                var isFirst = true;
                foreach (var arg in selection.Args)
                {
                    if (!isFirst)
                    {
                        builder.Append(',');
                    }
                    builder.Append(arg.Key);
                    builder.Append(':');
                    builder.Append(arg.FormatValue().Result);
                    isFirst = false;
                }
                builder.Append(')');
            }
        }
        builder.Append('}', Path.Count);
        return builder.ToString();
    }

    /// <summary>
    /// Create a query builder.
    /// </summary>
    /// <returns>A QueryBuilder instance.</returns>
    public static QueryBuilder Builder()
    {
        return new QueryBuilder();
    }
}
