using System.Net.Http.Headers;
using System.Text;
using System.Text.Json;
using Dagger.Telemetry;

namespace Dagger.GraphQL;

/// <summary>
/// GraphQL client for Dagger.
/// </summary>
public class GraphQLClient
{
    private readonly HttpClient _httpClient;

    /// <summary>
    /// Initializes a new GraphQL client using environment variables.
    /// </summary>
    public GraphQLClient()
        : this(
            Environment.GetEnvironmentVariable("DAGGER_SESSION_PORT")!,
            Environment.GetEnvironmentVariable("DAGGER_SESSION_TOKEN")!
        ) { }

    /// <summary>
    /// Initializes a new GraphQL client with specified connection details.
    /// </summary>
    /// <param name="port">The session port.</param>
    /// <param name="token">The session token.</param>
    /// <param name="scheme">The URL scheme (default: http).</param>
    /// <param name="host">The host address (default: localhost).</param>
    public GraphQLClient(
        string port,
        string token,
        string scheme = "http",
        string host = "localhost"
    )
    {
        _httpClient = new HttpClient();
        _httpClient.DefaultRequestHeaders.Authorization = new AuthenticationHeaderValue(
            "Basic",
            GetTokenHeaderValue(token)
        );
        _httpClient.DefaultRequestHeaders.Accept.Add(
            new MediaTypeWithQualityHeaderValue("application/json")
        );
        _httpClient.BaseAddress = new Uri($"{scheme}://{host}:{port}");
    }

    private static string GetTokenHeaderValue(string token) =>
        Convert.ToBase64String(Encoding.UTF8.GetBytes($"{token}:"));

    /// <summary>
    /// Perform GraphQL request.
    /// </summary>
    /// <param name="query">GraphQL query.</param>
    /// <param name="cancellationToken">Cancellation token.</param>
    /// <returns>Raw HTTP response.</returns>
    public async Task<HttpResponseMessage> RequestAsync(
        string query,
        CancellationToken cancellationToken = default
    )
    {
        var content = new StringContent(
            JsonSerializer.Serialize(new { query }),
            Encoding.UTF8,
            "application/json"
        );

        // Propagate W3C trace context if present
        var traceParent = TracePropagation.GetTraceParent();
        if (!string.IsNullOrEmpty(traceParent))
        {
            content.Headers.Add("traceparent", traceParent);
        }

        return await _httpClient.PostAsync("/query", content, cancellationToken);
    }
}
