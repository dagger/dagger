using System.Text.Json.Serialization;

namespace Dagger.SDK.SourceGenerator.Types;

public class InputValue
{
    [JsonPropertyName("name")]
    public required string Name { get; set; }

    [JsonPropertyName("description")]
    public string Description { get; set; } = "";

    [JsonPropertyName("type")]
    public required TypeRef Type { get; set; }

    [JsonPropertyName("directives")]
    public Directive[] Directives { get; set; } = [];

    [JsonPropertyName("defaultValue")]
    public string? DefaultValue { get; set; }

    /// <summary>
    /// Get the @expectedType name from this arg's directives, if present.
    /// </summary>
    public string? GetExpectedType()
    {
        foreach (var d in Directives)
        {
            var et = d.GetExpectedType();
            if (et != null) return et;
        }
        return null;
    }
}
