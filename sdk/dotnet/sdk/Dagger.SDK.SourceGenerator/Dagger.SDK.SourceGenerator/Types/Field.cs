using System.Collections.Generic;
using System.Collections.Immutable;
using System.Linq;
using System.Text.Json.Serialization;

namespace Dagger.SDK.SourceGenerator.Types;

public class Field
{
    [JsonPropertyName("name")]
    public required string Name { get; set; }

    [JsonPropertyName("description")]
    public required string Description { get; set; }

    [JsonPropertyName("type")]
    public required TypeRef Type { get; set; }

    [JsonPropertyName("args")]
    public required InputValue[] Args { get; set; }

    [JsonPropertyName("isDeprecated")]
    public bool IsDeprecated { get; set; }

    [JsonPropertyName("deprecationReason")]
    public required string DeprecationReason { get; set; }

    /// <summary>
    /// Get optional arguments from Args.
    /// </summary>
    public ImmutableArray<InputValue> OptionalArgs() =>
        Args.Where(arg => arg.Type.Kind != "NON_NULL").ToImmutableArray();

    /// <summary>
    /// Get required arguments from Args.
    /// </summary>
    public ImmutableArray<InputValue> RequiredArgs() =>
        Args.Where(arg => arg.Type.Kind == "NON_NULL").ToImmutableArray();
}
