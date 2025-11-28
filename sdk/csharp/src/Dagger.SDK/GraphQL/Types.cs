using System.Globalization;
using System.Text;

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
    public abstract Task<string> FormatAsync(CancellationToken cancellationToken = default);
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
    public override async Task<string> FormatAsync(CancellationToken cancellationToken = default)
    {
        var id = await value.IdAsync(cancellationToken);
        return await new StringValue(id.Value).FormatAsync(cancellationToken);
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

/// <summary>
/// Represents a GraphQL integer value.
/// </summary>
/// <param name="n">The integer value.</param>
public class IntValue(int n) : Value
{
    /// <summary>
    /// Formats the integer value as a GraphQL string.
    /// </summary>
    /// <param name="cancellationToken">A cancellation token.</param>
    /// <returns>The formatted integer value.</returns>
    public override Task<string> FormatAsync(CancellationToken cancellationToken = default)
    {
        return Task.FromResult(n.ToString());
    }
}

/// <summary>
/// Represents a GraphQL float value.
/// </summary>
/// <param name="f">The float value.</param>
public class FloatValue(float f) : Value
{
    /// <summary>
    /// Formats the float value as a GraphQL string.
    /// </summary>
    /// <param name="cancellationToken">A cancellation token.</param>
    /// <returns>The formatted float value.</returns>
    public override Task<string> FormatAsync(CancellationToken cancellationToken = default)
    {
        return Task.FromResult(f.ToString(CultureInfo.CurrentCulture));
    }
}

/// <summary>
/// Represents a GraphQL boolean value.
/// </summary>
/// <param name="b">The boolean value.</param>
public class BooleanValue(bool b) : Value
{
    const string TRUE = "true";
    const string FALSE = "false";

    /// <summary>
    /// Formats the boolean value as a GraphQL string.
    /// </summary>
    /// <param name="cancellationToken">A cancellation token.</param>
    /// <returns>The formatted boolean value.</returns>
    public override Task<string> FormatAsync(CancellationToken cancellationToken = default)
    {
        return Task.FromResult(b ? TRUE : FALSE);
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
