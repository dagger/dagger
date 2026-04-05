using System.Text.Json.Serialization;

namespace Dagger.SDK.CodeGen.Types;

public class InputValue
{
    [JsonPropertyName("name")]
    public required string Name { get; set; }

    [JsonPropertyName("description")]
    public required string Description { get; set; }

    [JsonPropertyName("type")]
    public required TypeRef Type { get; set; }

    [JsonPropertyName("defaultValue")]
    public string? DefaultValue { get; set; }

    [JsonPropertyName("directives")]
    public Directive[]? Directives { get; set; }
}
