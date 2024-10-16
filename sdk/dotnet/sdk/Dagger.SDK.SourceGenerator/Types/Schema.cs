using System.Text.Json.Serialization;

namespace Dagger.SDK.SourceGenerator.Types;

public class Schema
{
    [JsonPropertyName("types")]
    public required Type[] Types { get; set; }
}
