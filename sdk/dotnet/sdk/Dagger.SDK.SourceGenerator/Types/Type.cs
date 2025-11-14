using System.Text.Json.Serialization;

namespace Dagger.SDK.SourceGenerator.Types;

public class Type
{
    [JsonPropertyName("description")]
    public string Description { get; set; } = "";

    [JsonPropertyName("enumValues")]
    public EnumValue[] EnumValues { get; set; } = [];

    [JsonPropertyName("fields")]
    public Field[] Fields { get; set; } = [];

    [JsonPropertyName("inputFields")]
    public InputValue[] InputFields { get; set; } = [];

    [JsonPropertyName("kind")]
    public required string Kind { get; set; }

    [JsonPropertyName("name")]
    public required string Name { get; set; }
}
