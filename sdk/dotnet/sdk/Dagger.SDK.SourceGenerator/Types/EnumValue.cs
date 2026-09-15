using System.Text.Json.Serialization;

namespace Dagger.SDK.SourceGenerator.Types;

public class EnumValue
{
    [JsonPropertyName("name")]
    public required string Name { get; set; }

    [JsonPropertyName("description")]
    public string Description { get; set; } = "";

    [JsonPropertyName("isDeprecated")]
    public bool IsDeprecated { get; set; }
}
