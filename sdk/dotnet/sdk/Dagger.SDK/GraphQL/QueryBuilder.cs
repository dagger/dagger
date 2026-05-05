using System.Collections.Immutable;
using System.Net;
using System.Text;

namespace Dagger.SDK.GraphQL;

/// <summary>
/// A builder for constructing GraphQL query.
/// </summary>
public class QueryBuilder(ImmutableList<Field> path, string? inlineFragmentType = null)
{
    public readonly ImmutableList<Field> Path = path;
    private readonly string? _inlineFragmentType = inlineFragmentType;

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

    public QueryBuilder Select(Field field)
    {
        return new QueryBuilder(Path.Add(field), _inlineFragmentType);
    }

    /// <summary>
    /// Add an inline fragment type condition.
    /// Subsequent selections will be nested inside `... on TypeName { }` at this level.
    /// </summary>
    public QueryBuilder InlineFragment(string typeName)
    {
        return new QueryBuilder(Path, typeName);
    }

    /// <summary>
    /// Build GraphQL query.
    /// </summary>
    /// <returns>GraphQL query string</returns>
    public string Build()
    {
        if (_inlineFragmentType != null)
        {
            return BuildWithInlineFragment();
        }

        var builder = new StringBuilder();
        builder.Append("query");
        foreach (var selection in Path)
        {
            builder.Append('{');
            builder.Append(selection.Name);
            if (selection.Args.Count > 0)
            {
                builder.Append('(');
                builder.Append(
                    string.Join(
                        ",",
                        selection.Args.Select(arg => $"{arg.Key}:{arg.FormatValue().Result}")
                    )
                );
                builder.Append(')');
            }
        }
        builder.Append(new string('}', Path.Count));
        return builder.ToString();
    }

    /// <summary>
    /// Build a GraphQL query with an inline fragment at position 0.
    /// Produces: query{node(id:"..."){...on TypeName{field1{field2}}}}
    /// </summary>
    private string BuildWithInlineFragment()
    {
        var builder = new StringBuilder();
        builder.Append("query");

        // The first selection is the node(id:) call
        if (Path.Count > 0)
        {
            var first = Path[0];
            builder.Append('{');
            builder.Append(first.Name);
            if (first.Args.Count > 0)
            {
                builder.Append('(');
                builder.Append(
                    string.Join(
                        ",",
                        first.Args.Select(arg => $"{arg.Key}:{arg.FormatValue().Result}")
                    )
                );
                builder.Append(')');
            }

            // Open inline fragment
            builder.Append("{...on ");
            builder.Append(_inlineFragmentType);

            // Remaining selections go inside the inline fragment
            for (int i = 1; i < Path.Count; i++)
            {
                var sel = Path[i];
                builder.Append('{');
                builder.Append(sel.Name);
                if (sel.Args.Count > 0)
                {
                    builder.Append('(');
                    builder.Append(
                        string.Join(
                            ",",
                            sel.Args.Select(arg => $"{arg.Key}:{arg.FormatValue().Result}")
                        )
                    );
                    builder.Append(')');
                }
            }

            // Close: inner selections + inline fragment + node
            builder.Append(new string('}', Path.Count - 1)); // inner selections
            builder.Append('}'); // close inline fragment
            builder.Append('}'); // close node
        }

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
