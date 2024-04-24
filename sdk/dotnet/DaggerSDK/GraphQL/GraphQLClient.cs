using System.Net.Http.Json;
using System.Text;

namespace DaggerSDK.GraphQL;

public class GraphQLClient
{
    private readonly HttpClient _http;

    public GraphQLClient()
    {
        var port = Environment.GetEnvironmentVariable("DAGGER_SESSION_PORT");
        var token = Environment.GetEnvironmentVariable("DAGGER_SESSION_TOKEN");
        token = Convert.ToBase64String(Encoding.UTF8.GetBytes(token + ":"));
        var url = $"http://localhost:{port}/query";

        _http = new HttpClient();
        _http.DefaultRequestHeaders.Add("Authorization", $"Basic {token}");
        _http.DefaultRequestHeaders.Add("Accept", "application/json");
        _http.BaseAddress = new Uri(url);
    }

    public async Task<HttpResponseMessage> RequestAsync(string body)
    {
        var content = JsonContent.Create(new { query = body });
        return await _http.PostAsync("", content);
    }
}
