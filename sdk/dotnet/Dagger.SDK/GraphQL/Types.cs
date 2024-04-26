using System.Text;

namespace Dagger.SDK.GraphQL;

public abstract class GraphQLType
{
    public abstract string Format();
}

public class StringType(string s) : GraphQLType
{
    private readonly string value = s;

    public override string Format()
    {
        return $"\"{value}\"";
    }
}

public class IntType(int n) : GraphQLType
{
    private readonly int value = n;

    public override string Format()
    {
        return value.ToString();
    }
}

public class FloatType(float f) : GraphQLType
{
    private readonly float value = f;

    public override string Format()
    {
        return value.ToString();
    }
}

public class BooleanType(bool f) : GraphQLType
{
    private readonly bool value = f;

    public override string Format()
    {
        if (value == true)
        {
            return "true";
        }
        else
        {
            return "false";
        }
    }
}

public class ListType(List<GraphQLType> list) : GraphQLType
{
    private readonly List<GraphQLType> value = list;

    public override string Format()
    {
        var builder = new StringBuilder();
        builder.Append('[');
        builder.Append(string.Join(",", value.Select(element => element.Format())));
        builder.Append(']');
        return builder.ToString();
    }
}

public class ObjecType(List<KeyValuePair<string, GraphQLType>> obj) : GraphQLType
{
    private readonly List<KeyValuePair<string, GraphQLType>> value = obj;

    public override string Format()
    {
        var builder = new StringBuilder();
        builder.Append('{');
        builder.Append(string.Join(",", value.Select(kv => $"{kv.Key}:{kv.Value.Format()}")));
        builder.Append('}');
        return builder.ToString();
    }
}
