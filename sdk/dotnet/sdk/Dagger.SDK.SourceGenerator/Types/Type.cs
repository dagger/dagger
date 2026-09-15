using System.Text.Json.Serialization;

namespace Dagger.SDK.SourceGenerator.Types;

public class Type
{
    [JsonPropertyName("description")]
    public string Description { get; set; } = "";

    [JsonPropertyName("directives")]
    public Directive[] Directives { get; set; } = [];

    [JsonPropertyName("enumValues")]
    public EnumValue[] EnumValues { get; set; } = [];

    [JsonPropertyName("fields")]
    public Field[] Fields { get; set; } = [];

    [JsonPropertyName("inputFields")]
    public InputValue[] InputFields { get; set; } = [];

    [JsonPropertyName("interfaces")]
    public TypeRef[] Interfaces { get; set; } = [];

    [JsonPropertyName("kind")]
    public required string Kind { get; set; }

    [JsonPropertyName("name")]
    public required string Name { get; set; }

    [JsonPropertyName("possibleTypes")]
    public TypeRef[] PossibleTypes { get; set; } = [];
}
