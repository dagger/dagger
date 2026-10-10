using System.Text.Json.Serialization;

namespace Dagger.SDK.SourceGenerator.Types;

public class Introspection
{
    [JsonPropertyName("__schemaVersion")]
    public string? SchemaVersion { get; set; }

    [JsonPropertyName("__schema")]
    public required Schema Schema { get; set; }
}
