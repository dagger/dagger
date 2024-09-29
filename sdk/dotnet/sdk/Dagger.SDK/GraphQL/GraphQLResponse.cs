using System.Text.Json;
using System.Text.Json.Serialization;

namespace Dagger.SDK.GraphQL;

public class GraphQLError
{
    [JsonPropertyName("message")]
    public string Message { get; set; }

    [JsonPropertyName("path")]
    public List<string> Path { get; set; }
}

public class GraphQLResponse
{
    [JsonPropertyName("errors")]
    public List<GraphQLError>? Errors { get; set; }

    [JsonPropertyName("data")]
    public JsonElement Data { get; set; }
}
