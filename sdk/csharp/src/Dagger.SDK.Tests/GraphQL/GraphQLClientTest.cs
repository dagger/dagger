using Dagger.GraphQL;

namespace Dagger.SDK.Tests.GraphQL;

[TestClass]
public class GraphQLClientTest
{
    [TestMethod]
    public async Task TestRequest()
    {
        var client = new GraphQLClient();
        var query = "query{container{from(address:\"alpine\"){id}}}";

        var response = await client.RequestAsync(query);

        Assert.IsNotNull(response);
        Assert.IsTrue(response.IsSuccessStatusCode);
    }

    [TestMethod]
    public async Task TestRequest_WithError()
    {
        var client = new GraphQLClient();
        var query = "query{invalid}";

        var response = await client.RequestAsync(query);

        Assert.IsNotNull(response);
        var content = await response.Content.ReadAsStringAsync();
        Assert.IsTrue(content.Contains("errors") || content.Contains("error"));
    }
}
