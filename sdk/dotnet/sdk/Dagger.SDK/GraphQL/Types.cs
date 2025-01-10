using System.Globalization;
using System.Text;

namespace Dagger.SDK.GraphQL;

public abstract class Value
{
    public abstract Task<string> FormatAsync(CancellationToken cancellationToken = default);
}

public class IdValue<TId>(IId<TId> value) : Value
    where TId : Scalar
{
    public override async Task<string> FormatAsync(CancellationToken cancellationToken = default)
    {
        var id = await value.IdAsync(cancellationToken);
        return await new StringValue(id.Value).FormatAsync(cancellationToken);
    }
}

public class StringValue(string value) : Value
{
    public override Task<string> FormatAsync(CancellationToken cancellationToken = default)
    {
        if (string.IsNullOrEmpty(value))
        {
            return Task.FromResult("\"\"");
        }

        var sb = new StringBuilder(value.Length + 2);
        sb.Append('"');

        foreach (char c in value)
        {
            switch (c)
            {
                case '\\':
                    sb.Append("\\\\");
                    break;
                case '\r':
                    sb.Append("\\r");
                    break;
                case '\n':
                    sb.Append("\\n");
                    break;
                case '\t':
                    sb.Append("\\t");
                    break;
                case '"':
                    sb.Append("\\\"");
                    break;
                default:
                    sb.Append(c);
                    break;
            }
        }

        sb.Append('"');
        return Task.FromResult(sb.ToString());
    }
}

public class IntValue(int n) : Value
{
    public override Task<string> FormatAsync(CancellationToken cancellationToken = default)
    {
        return Task.FromResult(n.ToString());
    }
}

public class FloatValue(float f) : Value
{
    public override Task<string> FormatAsync(CancellationToken cancellationToken = default)
    {
        return Task.FromResult(f.ToString(CultureInfo.CurrentCulture));
    }
}

public class BooleanValue(bool b) : Value
{
    const string TRUE = "true";
    const string FALSE = "false";

    public override Task<string> FormatAsync(CancellationToken cancellationToken = default)
    {
        return Task.FromResult(b ? TRUE : FALSE);
    }
}

public class ListValue(List<Value> list) : Value
{
    public override async Task<string> FormatAsync(CancellationToken cancellationToken = default)
    {
        Task<string>[] tasks = list.Select(element => element.FormatAsync(cancellationToken))
            .ToArray();
        string[] results = await Task.WhenAll(tasks);

        var builder = new StringBuilder();
        builder.Append('[');
        builder.Append(string.Join(",", results.Select(v => v)));
        builder.Append(']');
        return builder.ToString();
    }
}

public class ObjectValue(List<KeyValuePair<string, Value>> obj) : Value
{
    public override async Task<string> FormatAsync(CancellationToken cancellationToken = default)
    {
        Task<string>[] tasks = obj.Select(kv => kv.Value.FormatAsync(cancellationToken)).ToArray();
        string[] results = await Task.WhenAll(tasks);

        var builder = new StringBuilder();
        builder.Append('{');
        builder.Append(string.Join(",", obj.Select((kv, i) => $"{kv.Key}:{results[i]}")));
        builder.Append('}');
        return builder.ToString();
    }
}
