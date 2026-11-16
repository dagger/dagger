using Npgsql;
using Xunit;

namespace IntegrationTestExample;

public class PostgresTest(PostgresFixture postgres) : IClassFixture<PostgresFixture>
{
    [Fact]
    public async Task StoresAndReadsARow()
    {
        await using var connection = new NpgsqlConnection(postgres.ConnectionString);
        await connection.OpenAsync(TestContext.Current.CancellationToken);

        await using var create = new NpgsqlCommand(
            "CREATE TABLE greeting (text text NOT NULL); INSERT INTO greeting VALUES ('hello from Dagger')",
            connection
        );
        await create.ExecuteNonQueryAsync(TestContext.Current.CancellationToken);

        await using var read = new NpgsqlCommand("SELECT text FROM greeting", connection);
        var text = await read.ExecuteScalarAsync(TestContext.Current.CancellationToken);

        Assert.Equal("hello from Dagger", text);
    }
}
