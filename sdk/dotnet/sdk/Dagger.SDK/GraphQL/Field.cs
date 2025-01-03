using System.Collections.Immutable;

namespace Dagger.SDK.GraphQL;

public class Argument(string key, Value value)
{
    public string Key { get; } = key;
    private Value Value { get; } = value;

    public Task<string> FormatValue(CancellationToken cancellationToken = default) =>
        Value.FormatAsync(cancellationToken);
}

public class Field(string name, ImmutableList<Argument> args)
{
    public string Name { get; } = name;

    public ImmutableList<Argument> Args { get; } = args;
}
