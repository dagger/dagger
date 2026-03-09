using System.Globalization;
using System.Text;
using Microsoft.Extensions.Primitives;

namespace Dagger.GraphQL;

/// <summary>
/// Base class for GraphQL values.
/// </summary>
public abstract class Value
{
    /// <summary>
    /// Formats this value as a GraphQL string.
    /// </summary>
    /// <param name="cancellationToken">A cancellation token.</param>
    /// <returns>The formatted value.</returns>
    public abstract ValueTask<string> FormatAsync(CancellationToken cancellationToken = default);
}

/// <summary>
/// Represents a Dagger ID value.
/// </summary>
/// <typeparam name="TId">The scalar ID type.</typeparam>
/// <param name="value">The ID value.</param>
public class IdValue<TId>(IId<TId> value) : Value
    where TId : Scalar
{
    /// <summary>
    /// Formats the ID value as a GraphQL string.
    /// </summary>
    /// <param name="cancellationToken">A cancellation token.</param>
    /// <returns>The formatted ID value.</returns>
    public override async ValueTask<string> FormatAsync(
        CancellationToken cancellationToken = default
    )
    {
        var id = await value.Id(cancellationToken).ConfigureAwait(false);
        return await new StringValue(id.Value).FormatAsync(cancellationToken).ConfigureAwait(false);
    }
}

/// <summary>
/// Represents a GraphQL string value.
/// </summary>
/// <param name="value">The string value.</param>
public class StringValue(string value) : Value
{
    /// <summary>
    /// Formats the string value as a GraphQL string.
    /// </summary>
    /// <param name="cancellationToken">A cancellation token.</param>
    /// <returns>The formatted string value.</returns>
    public override ValueTask<string> FormatAsync(CancellationToken cancellationToken = default)
    {
        if (string.IsNullOrEmpty(value))
        {
            return new ValueTask<string>("\"\"");
        }

        var sb = new StringBuilder(value.Length + 16);
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
        return new ValueTask<string>(sb.ToString());
    }
}

/// <summary>
/// Represents a GraphQL integer value.
/// </summary>
/// <param name="n">The integer value.</param>
public class IntValue(int n) : Value
{
    private string? _cached;

    /// <summary>
    /// Formats the integer value as a GraphQL string.
    /// </summary>
    /// <param name="cancellationToken">A cancellation token.</param>
    /// <returns>The formatted integer value.</returns>
    public override ValueTask<string> FormatAsync(CancellationToken cancellationToken = default)
    {
        _cached ??= n.ToString();
        return new ValueTask<string>(_cached);
    }
}

/// <summary>
/// Represents a GraphQL float value.
/// </summary>
/// <param name="f">The float value.</param>
public class FloatValue(float f) : Value
{
    private string? _cached;

    /// <summary>
    /// Formats the float value as a GraphQL string.
    /// </summary>
    /// <param name="cancellationToken">A cancellation token.</param>
    /// <returns>The formatted float value.</returns>
    public override ValueTask<string> FormatAsync(CancellationToken cancellationToken = default)
    {
        _cached ??= f.ToString(CultureInfo.InvariantCulture);
        return new ValueTask<string>(_cached);
    }
}

/// <summary>
/// Represents a GraphQL boolean value.
/// </summary>
/// <param name="b">The boolean value.</param>
public class BooleanValue(bool b) : Value
{
    /// <summary>
    /// Formats the boolean value as a GraphQL string.
    /// </summary>
    /// <param name="cancellationToken">A cancellation token.</param>
    /// <returns>The formatted boolean value.</returns>
    public override ValueTask<string> FormatAsync(CancellationToken cancellationToken = default)
    {
        return new ValueTask<string>(b ? "true" : "false");
    }
}

/// <summary>
/// Represents a GraphQL list value.
/// </summary>
/// <param name="list">The list of values.</param>
public class ListValue(List<Value> list) : Value
{
    /// <summary>
    /// Formats the list value as a GraphQL string.
    /// </summary>
    /// <param name="cancellationToken">A cancellation token.</param>
    /// <returns>The formatted list value.</returns>
    public override async ValueTask<string> FormatAsync(
        CancellationToken cancellationToken = default
    )
    {
        if (list.Count == 0)
        {
            return "[]";
        }

        var tasks = list.Select(element => element.FormatAsync(cancellationToken).AsTask())
            .ToArray();
        var results = await Task.WhenAll(tasks).ConfigureAwait(false);

        var builder = new StringBuilder();
        builder.Append('[');
        builder.Append(string.Join(",", results));
        builder.Append(']');
        return builder.ToString();
    }
}

/// <summary>
/// Represents a GraphQL object value.
/// </summary>
/// <param name="obj">The object key-value pairs.</param>
public class ObjectValue(List<KeyValuePair<string, Value>> obj) : Value
{
    /// <summary>
    /// Formats the object value as a GraphQL string.
    /// </summary>
    /// <param name="cancellationToken">A cancellation token.</param>
    /// <returns>The formatted object value.</returns>
    public override async ValueTask<string> FormatAsync(
        CancellationToken cancellationToken = default
    )
    {
        if (obj.Count == 0)
        {
            return "{}";
        }

        var tasks = obj.Select(kv => kv.Value.FormatAsync(cancellationToken).AsTask()).ToArray();
        var results = await Task.WhenAll(tasks).ConfigureAwait(false);

        var builder = new StringBuilder();
        builder.Append('{');
        for (int i = 0; i < obj.Count; i++)
        {
            if (i > 0)
            {
                builder.Append(',');
            }
            builder.Append(obj[i].Key).Append(':').Append(results[i]);
        }
        builder.Append('}');
        return builder.ToString();
    }
}
