using System.Text.Json.Serialization;

namespace Dagger.SDK.CodeGen.Types;

public class Schema
{
    [JsonPropertyName("types")]
    public required Type[] Types { get; set; }
}
