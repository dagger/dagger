using System.Collections.Immutable;
using System.Net;
using System.Text;

namespace Dagger.SDK.GraphQL;

public class QueryBuilder
{
    public readonly ImmutableList<Field> Path = [];

    public QueryBuilder() { }

    public QueryBuilder(ImmutableList<Field> children)
    {
        this.Path = children;
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
                builder.Append(string.Join(",", selection.Args.Select(arg => $"{arg.Key}:{arg.FormatValue()}")));
                builder.Append(')');
            }
        }
        builder.Append(new string('}', Path.Count));
        return builder.ToString();
    }

    public static QueryBuilder Builder()
    {
        return new QueryBuilder();
    }
}
