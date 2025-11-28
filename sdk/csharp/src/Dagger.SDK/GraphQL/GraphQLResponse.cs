using System.Text.Json;
using System.Text.Json.Serialization;

namespace Dagger.GraphQL;

/// <summary>
/// Represents a GraphQL error from the server.
/// </summary>
public class GraphQLError
{
    /// <summary>
    /// The error message.
    /// </summary>
    [JsonPropertyName("message")]
    public string Message { get; set; } = string.Empty;

    /// <summary>
    /// The path to the field that caused the error.
    /// </summary>
    [JsonPropertyName("path")]
    public List<string>? Path { get; set; }

    /// <summary>
    /// The locations in the query where the error occurred.
    /// </summary>
    [JsonPropertyName("locations")]
    public List<GraphQLErrorLocation>? Locations { get; set; }

    /// <summary>
    /// Additional error metadata.
    /// </summary>
    [JsonPropertyName("extensions")]
    public Dictionary<string, JsonElement>? Extensions { get; set; }

    /// <summary>
    /// Gets the error type from extensions, if available.
    /// </summary>
    public string? ErrorType
    {
        get
        {
            return Extensions?.TryGetValue("_type", out var type) == true
        ? type.GetString()
        : null;
        }
    }
}

/// <summary>
/// Represents the location of an error in a GraphQL query.
/// </summary>
public class GraphQLErrorLocation
{
    /// <summary>
    /// The line number.
    /// </summary>
    [JsonPropertyName("line")]
    public int Line { get; set; }

    /// <summary>
    /// The column number.
    /// </summary>
    [JsonPropertyName("column")]
    public int Column { get; set; }
}

/// <summary>
/// Represents a GraphQL response from the server.
/// </summary>
public class GraphQLResponse
{
    /// <summary>
    /// Any errors that occurred during the query.
    /// </summary>
    [JsonPropertyName("errors")]
    public List<GraphQLError>? Errors { get; set; }

    /// <summary>
    /// The query response data.
    /// </summary>
    [JsonPropertyName("data")]
    public JsonElement Data { get; set; }
}
