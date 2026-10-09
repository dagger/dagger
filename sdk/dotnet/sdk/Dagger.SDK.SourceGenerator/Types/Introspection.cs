using System.Collections.Generic;
using System.Text.Json.Serialization;

namespace Dagger.SDK.SourceGenerator.Types;

public class Introspection
{
    [JsonPropertyName("__schemaVersion")]
    public string? SchemaVersion { get; set; }

    [JsonPropertyName("__schema")]
    public required Schema Schema { get; set; }

    /// <summary>
    /// The words of the schema's names. Absent for engines before v1.0.0.
    /// </summary>
    [JsonPropertyName("__identifiers")]
    public Dictionary<string, IdentifierWord[]>? Identifiers { get; set; }
}
