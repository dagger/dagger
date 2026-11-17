using System.Net.Http.Headers;
using System.Text;
using System.Text.Json;
using Dagger.Exceptions;
using Dagger.Telemetry;

namespace Dagger.GraphQL;

/// <summary>
/// GraphQL client for Dagger.
/// </summary>
public class GraphQLClient : IDisposable
{
    internal const string SessionPortVariable = "DAGGER_SESSION_PORT";
    internal const string SessionTokenVariable = "DAGGER_SESSION_TOKEN";

    private readonly HttpClient _httpClient;
    private readonly CliSession? _session;
    private bool _disposed;

    /// <summary>
    /// Initializes a new GraphQL client for the session the environment provides
    /// (<c>DAGGER_SESSION_PORT</c> and <c>DAGGER_SESSION_TOKEN</c>, as set by <c>dagger run</c>
    /// and inside Dagger containers). Without one it starts <c>dagger session</c> itself, with
    /// the CLI named by <c>_EXPERIMENTAL_DAGGER_CLI_BIN</c> or else the CLI release this SDK is
    /// built for, which is downloaded on first use. That session ends when this client is
    /// disposed or the process exits.
    /// </summary>
    /// <exception cref="Dagger.Exceptions.DaggerException">Thrown when no session is provided and none can be started.</exception>
    public GraphQLClient()
        : this(ProvidedSession() ? null : CliSession.Start()) { }

    private GraphQLClient(CliSession? session)
        : this(
            session?.Port ?? Environment.GetEnvironmentVariable(SessionPortVariable)!,
            session?.Token ?? Environment.GetEnvironmentVariable(SessionTokenVariable)!,
            host: "127.0.0.1"
        )
    {
        _session = session;
    }

    private static bool ProvidedSession()
    {
        var port = Environment.GetEnvironmentVariable(SessionPortVariable);
        if (string.IsNullOrEmpty(port))
        {
            return false;
        }

        if (!int.TryParse(port, out _))
        {
            throw new DaggerException($"{SessionPortVariable} is not a port number: {port}");
        }

        if (string.IsNullOrEmpty(Environment.GetEnvironmentVariable(SessionTokenVariable)))
        {
            throw new DaggerException(
                $"{SessionTokenVariable} must be set when using {SessionPortVariable}."
            );
        }

        return true;
    }

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
        // Disable DiagnosticsHandler's automatic HttpRequestOut activities:
        // they inject a never-exported ghost traceparent into request headers,
        // which the engine reads before our own header and mis-parents all
        // engine-side work into a foreign trace.
        //
        // No request timeout: one query can wait for a long exec or a service, far beyond
        // HttpClient's default of 100 seconds. Callers bound a call with its CancellationToken.
        _httpClient = new HttpClient(new SocketsHttpHandler { ActivityHeadersPropagator = null })
        {
            Timeout = Timeout.InfiniteTimeSpan,
        };
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

        var request = new HttpRequestMessage(HttpMethod.Post, "/query") { Content = content };

        // Propagate W3C trace context on the request headers (the engine reads
        // request headers before content headers), preferring the live span so
        // engine-side work parents under the current invocation.
        var traceParent =
            System.Diagnostics.Activity.Current?.Id ?? TracePropagation.GetTraceParent();
        if (!string.IsNullOrEmpty(traceParent))
        {
            request.Headers.TryAddWithoutValidation("traceparent", traceParent);
        }

        return await _httpClient.SendAsync(request, cancellationToken).ConfigureAwait(false);
    }

    /// <summary>
    /// Releases the unmanaged resources used by the GraphQLClient.
    /// </summary>
    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }

        _httpClient?.Dispose();
        _session?.Dispose();
        _disposed = true;
    }
}
