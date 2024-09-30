using System.Text.Json.Serialization;

namespace Dagger.SDK.SourceGenerator.Types;

public class Type
{
    [JsonPropertyName("description")]
    public required string Description { get; set; }

    [JsonPropertyName("enumValues")]
    public required EnumValue[] EnumValues { get; set; }

    [JsonPropertyName("fields")]
    public required Field[] Fields { get; set; }

    [JsonPropertyName("inputFields")]
    public required InputValue[] InputFields { get; set; }

    [JsonPropertyName("kind")]
    public required string Kind { get; set; }

    [JsonPropertyName("name")]
    public required string Name { get; set; }
}
