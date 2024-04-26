using System.Text.Json;
using System.Text.Json.Serialization;

namespace Dagger.SDK.GraphQL;

public class GraphQLError
{
    [JsonPropertyName("message")]
    public required string Message { get; set; }

    [JsonPropertyName("path")]
    public required List<string> Path { get; set; }
}

public class GraphQLResponse
{
    [JsonPropertyName("errors")]
    public List<GraphQLError>? Errors { get; set; }

    [JsonPropertyName("data")]
    public required JsonElement Data { get; set; }
}
