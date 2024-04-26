using System.Collections.Immutable;
using System.Net;
using System.Text;

namespace Dagger.SDK.GraphQL;

public class QueryBuilder
{
    private readonly ImmutableList<Field> children = [];

    public QueryBuilder() { }

    public QueryBuilder(ImmutableList<Field> children)
    {
        this.children = children;
    }

    public QueryBuilder Select(string name)
    {
        return Select(name, []);
    }

    public QueryBuilder Select(string name, ImmutableList<Argument> args)
    {
        return Select(new Field(name, args));
    }

    public QueryBuilder Select(Field field)
    {
        return new QueryBuilder(children.Add(field));
    }

    /// <summary>
    /// Build GraphQL query.
    /// </summary>
    /// <returns>GraphQL query string</returns>
    public string Build()
    {
        var builder = new StringBuilder();
        builder.Append("query");
        foreach (var selection in children)
        {
            builder.Append('{');
            builder.Append(selection.Name);
            if (selection.Args.Count > 0)
            {
                builder.Append('(');
                builder.Append(string.Join(",", selection.Args.Select(arg => $"{arg.Key}:{arg.FormatValue()}")));
                builder.Append(')');
            }
        }
        builder.Append(new string('}', children.Count));
        return builder.ToString();
    }

    public static QueryBuilder Builder()
    {
        return new QueryBuilder();
    }
}
