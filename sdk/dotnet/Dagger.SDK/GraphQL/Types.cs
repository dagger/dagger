using System.Text;

namespace Dagger.SDK.GraphQL;

public abstract class Value
{
    public abstract string Format();
}

public class StringValue(string s) : Value
{
    private readonly string value = s;

    public override string Format()
    {
        return $"\"{value}\"";
    }
}

public class IntValue(int n) : Value
{
    private readonly int value = n;

    public override string Format()
    {
        return value.ToString();
    }
}

public class FloatValue(float f) : Value
{
    private readonly float value = f;

    public override string Format()
    {
        return value.ToString();
    }
}

public class BooleanValue(bool f) : Value
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

public class ListValue(List<Value> list) : Value
{
    private readonly List<Value> value = list;

    public override string Format()
    {
        var builder = new StringBuilder();
        builder.Append('[');
        builder.Append(string.Join(",", value.Select(element => element.Format())));
        builder.Append(']');
        return builder.ToString();
    }
}

public class ObjectValue(List<KeyValuePair<string, Value>> obj) : Value
{
    private readonly List<KeyValuePair<string, Value>> value = obj;

    public override string Format()
    {
        var builder = new StringBuilder();
        builder.Append('{');
        builder.Append(string.Join(",", value.Select(kv => $"{kv.Key}:{kv.Value.Format()}")));
        builder.Append('}');
        return builder.ToString();
    }
}
