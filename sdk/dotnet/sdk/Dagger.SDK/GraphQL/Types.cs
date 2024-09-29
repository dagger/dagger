using System.Globalization;
using System.Text;

namespace Dagger.SDK.GraphQL;

public abstract class Value
{
    public abstract Task<string> Format();
}

public class IdValue<TId>(IId<TId> value) : Value
    where TId : Scalar
{
    public override async Task<string> Format()
    {
        return await new StringValue((await value.Id()).Value).Format();
    }
}

public class StringValue(string value) : Value
{
    public override Task<string> Format()
    {
        var s = value
            .Replace("\\", @"\\")
            .Replace("\r", "\\r")
            .Replace("\n", "\\n")
            .Replace("\t", "\\t")
            .Replace("\"", "\\\"");
        return Task.FromResult($"\"{s}\"");
    }
}

public class IntValue(int n) : Value
{
    public override Task<string> Format()
    {
        return Task.FromResult(n.ToString());
    }
}

public class FloatValue(float f) : Value
{
    public override Task<string> Format()
    {
        return Task.FromResult(f.ToString(CultureInfo.CurrentCulture));
    }
}

public class BooleanValue(bool b) : Value
{
    public override Task<string> Format()
    {
        return Task.FromResult(b ? "true" : "false");
    }
}

public class ListValue(List<Value> list) : Value
{
    public override Task<string> Format()
    {
        var builder = new StringBuilder();
        builder.Append('[');
        builder.Append(
            string.Join(
                ",",
                list.Select(async element => await element.Format()).Select(v => v.Result)
            )
        );
        builder.Append(']');
        return Task.FromResult(builder.ToString());
    }
}

public class ObjectValue(List<KeyValuePair<string, Value>> obj) : Value
{
    public override Task<string> Format()
    {
        var builder = new StringBuilder();
        builder.Append('{');
        builder.Append(
            string.Join(
                ",",
                obj.Select(async kv => $"{kv.Key}:{await kv.Value.Format()}").Select(v => v.Result)
            )
        );
        builder.Append('}');
        return Task.FromResult(builder.ToString());
    }
}
