using System.Net;
using System.Net.Http.Json;
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

        var gqlResponse = await response.Content.ReadFromJsonAsync<GraphQLResponse>();
        Assert.Null(gqlResponse!.Errors);
        Assert.Equal("hello\n", gqlResponse!.Data.GetProperty("container").GetProperty("from").GetProperty("withExec").GetProperty("stdout").GetString());
    }
}
