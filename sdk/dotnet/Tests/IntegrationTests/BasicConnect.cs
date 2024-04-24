using DaggerSDK.GraphQL;
using IntegrationTests.TestData;
using Newtonsoft.Json;

namespace IntegrationTests;

public class BasicTests
{
    [Test]
    public async Task BasicConnectAsync()
    {
        var query = LaravelExample.RuntimeQuery;
        var client = new GraphQLClient();
        var response = await client.RequestAsync(query);
        var body = await response.Content.ReadAsStringAsync();
        var json = JsonConvert.DeserializeObject(body);
        Console.WriteLine(JsonConvert.SerializeObject(json, Formatting.Indented));
        Assert.That((int)response.StatusCode, Is.EqualTo(200));
    }

    [Test]
    public void CreateQuery()
    {
        var e = LaravelExample.RuntimeQueryElement;
        var q = Serializer.Serialize(e).Replace("\\u0027", "'");
        Console.WriteLine(q);
        Assert.That(q, Is.EqualTo(LaravelExample.RuntimeQuery.Replace("\r\n", "\n").Replace("    ", "  ")));
    }

    [Test]
    public void ContainerBuilder()
    {
        var builder = LaravelExample.ContainerBuilder;
        var builderQuery = Serializer.Serialize(builder.GetQuery());

        var compare = LaravelExample.RuntimeQueryElement;
        var expectedResult = Serializer.Serialize(compare);

        Assert.That(builderQuery, Is.EqualTo(expectedResult));
    }
}
