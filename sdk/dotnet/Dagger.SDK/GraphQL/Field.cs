using System.Collections.Immutable;

namespace Dagger.SDK.GraphQL;

public class Argument(string key, Value value)
{
    public string Key { get; } = key;
    public Value Value { get; } = value;

    public string FormatValue()
    {
        return Value.Format();
    }
}

public class Field(string name, ImmutableList<Argument> args)
{
    public string Name { get; } = name;

    public ImmutableList<Argument> Args { get; } = args;
}
