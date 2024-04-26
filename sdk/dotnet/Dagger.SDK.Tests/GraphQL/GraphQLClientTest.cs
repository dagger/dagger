using System.Net;
using System.Net.Http.Json;
using System.Security.Cryptography;
using System.Text.Json;
using Dagger.SDK.GraphQL;

namespace Dagger.SDK.Tests.GraphQL;

public class GraphQLClientTest
{
    [Fact]
    public async void TestRequest()
    {
        var query = """
        query {
            container {
                from(address: "alpine:3.16") {
                    withExec(args: ["echo", "hello"]) {
                        stdout
                    }
                }
            }
        }
        """;

        var gqlCLient = new GraphQLClient();
        var response = await gqlCLient.RequestAsync(query);

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);

        JsonElement data = await response.Content.ReadFromJsonAsync<JsonElement>();

        Assert.Equal("hello\n", data.GetProperty("data").GetProperty("container").GetProperty("from").GetProperty("withExec").GetProperty("stdout").GetString());
    }
}
