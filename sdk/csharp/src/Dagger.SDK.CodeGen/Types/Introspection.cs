using System.Text.Json.Serialization;

namespace Dagger.SDK.CodeGen.Types;

public class Introspection
{
    [JsonPropertyName("__schema")]
    public required Schema Schema { get; set; }
}
