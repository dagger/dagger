using Dagger;
using Xunit;
using static Dagger.Client;

namespace IntegrationTestExample;

/// <summary>
/// Starts PostgreSQL as a Dagger service once for all tests of a class and stops it afterwards.
/// </summary>
public sealed class PostgresFixture : IAsyncLifetime
{
    private const int Port = 5432;
    private const string Password = "example";

    private Service? _tunnel;

    public string ConnectionString { get; private set; } = string.Empty;

    public async ValueTask InitializeAsync()
    {
        var postgres = Dag.Container()
            .From("postgres:17-alpine")
            .WithEnvVariable("POSTGRES_PASSWORD", Password)
            .WithExposedPort(Port)
            .AsService(useEntrypoint: true);

        // The tunnel forwards a free local port to the service, so the tests reach it on
        // localhost whether they run on a developer machine or inside a Dagger container.
        _tunnel = Dag.Host().Tunnel(postgres);
        await _tunnel.Start();

        var endpoint = await _tunnel.Endpoint();
        var separator = endpoint.LastIndexOf(':');

        ConnectionString =
            $"Host={endpoint[..separator]};Port={endpoint[(separator + 1)..]};"
            + $"Username=postgres;Password={Password};Database=postgres";
    }

    public async ValueTask DisposeAsync()
    {
        if (_tunnel is not null)
        {
            await _tunnel.Stop();
        }
    }
}
