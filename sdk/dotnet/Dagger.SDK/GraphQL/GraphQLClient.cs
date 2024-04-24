
using System.Net.Http.Headers;
using System.Net.Http.Json;
using System.Text;

namespace Dagger.SDK.GraphQL;

/// <summary>
/// GraphQL client for Dagger.
/// </summary>
public class GraphQLClient
{
    private readonly HttpClient _httpClient;

    public GraphQLClient() : this(Environment.GetEnvironmentVariable("DAGGER_SESSION_PORT")!, Environment.GetEnvironmentVariable("DAGGER_SESSION_TOKEN")!)
    {
    }

    public GraphQLClient(string port, string token, string scheme = "http", string host = "localhost")
    {
        _httpClient = new HttpClient();
        _httpClient.DefaultRequestHeaders.Add("Authorization", BasicAuth(token));
        _httpClient.DefaultRequestHeaders.Add("Accept", "application/json");
        _httpClient.BaseAddress = new Uri($"{scheme}://{host}:{port}");
    }

    private static string BasicAuth(string token)
    {
        var usernamePassword = Convert.ToBase64String(Encoding.UTF8.GetBytes($"{token}:"));
        return $"Basic {usernamePassword}";
    }

    /// <summary>
    /// Perform GraphQL request.
    /// </summary>
    /// <param name="query">GraphQL query.</param>
    /// <returns>Raw HTTP response.</returns>
    public async Task<HttpResponseMessage> RequestAsync(string query)
    {
        return await _httpClient.PostAsJsonAsync("/query", new { query });
    }
}
