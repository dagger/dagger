using System.Net;
using System.Net.Http.Json;
using System.Security.Cryptography;
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

        dynamic? data = await response.Content.ReadFromJsonAsync(new
        {
            data = new
            {
                container = new
                {
                    from = new
                    {
                        withExec = new { stdout = "" }
                    }
                }
            }
        }.GetType());

        Assert.Equal("hello\n", data!.data.container.from.withExec.stdout);
    }
}
