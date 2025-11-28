namespace Dagger.GraphQL;

/// <summary>
/// Represents a GraphQL argument with a key and value.
/// </summary>
/// <param name="key">The argument name.</param>
/// <param name="value">The argument value.</param>
public class Argument(string key, Value value)
{
    /// <summary>
    /// The argument name.
    /// </summary>
    public string Key { get; } = key;
    private Value Value { get; } = value;

    /// <summary>
    /// Formats the argument value as a GraphQL string.
    /// </summary>
    /// <param name="cancellationToken">A cancellation token.</param>
    /// <returns>The formatted value string.</returns>
    public Task<string> FormatValue(CancellationToken cancellationToken = default) =>
        Value.FormatAsync(cancellationToken);
}
