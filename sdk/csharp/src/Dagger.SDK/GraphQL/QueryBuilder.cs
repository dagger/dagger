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
        : this([]) { }

    /// <summary>
    /// Select a field with name.
    /// </summary>
    /// <param name="name">The field name.</param>
    /// <returns>A new QueryBuilder instance.</returns>
    public QueryBuilder Select(string name) => Select(name, []);

    /// <summary>
    /// Select a field with name plus arguments.
    /// </summary>
    /// <param name="name">The field name.</param>
    /// <param name="args">The field arguments.</param>
    /// <returns>A new QueryBuilder instance.</returns>
    public QueryBuilder Select(string name, ImmutableList<Argument> args) =>
        Select(new Field(name, args));

    /// <summary>
    /// Select a field.
    /// </summary>
    /// <param name="field">The field to select.</param>
    /// <returns>A new QueryBuilder instance.</returns>
    public QueryBuilder Select(Field field) => new(Path.Add(field));

    /// <summary>
    /// Build GraphQL query.
    /// </summary>
    /// <param name="cancellationToken">A cancellation token.</param>
    /// <returns>GraphQL query string</returns>
    public async ValueTask<string> BuildAsync(CancellationToken cancellationToken = default)
    {
        var builder = new StringBuilder();
        builder.Append("query");
        foreach (var selection in Path)
        {
            builder.Append('{').Append(selection.Name);
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

                    var formattedValue = await arg.FormatValue(cancellationToken)
                        .ConfigureAwait(false);
                    builder.Append(arg.Key).Append(':').Append(formattedValue);
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
    public static QueryBuilder Builder() => new();
}
