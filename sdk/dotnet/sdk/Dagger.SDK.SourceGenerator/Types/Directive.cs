using System.Text.Json.Serialization;

namespace Dagger.SDK.SourceGenerator.Types;

public class DirectiveArg
{
    [JsonPropertyName("name")]
    public required string Name { get; set; }

    [JsonPropertyName("value")]
    public required string Value { get; set; }
}

public class Directive
{
    [JsonPropertyName("name")]
    public required string Name { get; set; }

    [JsonPropertyName("args")]
    public DirectiveArg[] Args { get; set; } = [];

    /// <summary>
    /// If this is an @expectedType directive, return the type name; otherwise null.
    /// </summary>
    public string? GetExpectedType()
    {
        if (Name != "expectedType") return null;
        foreach (var arg in Args)
        {
            if (arg.Name == "name")
            {
                // Value comes as "\"Container\"" — strip surrounding quotes
                return arg.Value.Trim('"');
            }
        }
        return null;
    }
}
