using Dagger.GraphQL;

namespace Dagger.SDK.Tests.GraphQL;

[TestClass]
public class QueryBuilderTest
{
    [TestMethod]
    public async Task TestSelect()
    {
        var query = await QueryBuilder.Builder().Select("container").BuildAsync();

        Assert.AreEqual("query{container}", query);
    }

    [TestMethod]
    public async Task TestSelect_WithArgument()
    {
        var query = await QueryBuilder
            .Builder()
            .Select("container")
            .Select("from", [new Argument("address", new StringValue("nginx"))])
            .BuildAsync();

        Assert.AreEqual("query{container{from(address:\"nginx\")}}", query);

        query = await QueryBuilder
            .Builder()
            .Select("container")
            .Select(
                "withExec",
                [
                    new Argument(
                        "args",
                        new ListValue([new StringValue("echo"), new StringValue("hello")])
                    ),
                ]
            )
            .BuildAsync();

        Assert.AreEqual("query{container{withExec(args:[\"echo\",\"hello\"])}}", query);

        query = await QueryBuilder
            .Builder()
            .Select(
                "buildDocker",
                [
                    new Argument(
                        "buildArgs",
                        new ObjectValue([
                            KeyValuePair.Create("key", new StringValue("value") as Value),
                        ])
                    ),
                ]
            )
            .BuildAsync();

        Assert.AreEqual("query{buildDocker(buildArgs:{key:\"value\"})}", query);

        query = await QueryBuilder
            .Builder()
            .Select("withEnvVariable", [new Argument("expand", new BooleanValue(true))])
            .BuildAsync();

        Assert.AreEqual("query{withEnvVariable(expand:true)}", query);
    }

    [TestMethod]
    public async Task TestSelect_ImmutableQuery()
    {
        var query1 = QueryBuilder.Builder().Select("envVariables");

        var query2 = await query1.Select("name").BuildAsync();

        Assert.AreEqual("query{envVariables{name}}", query2);

        var query3 = await query1.Select("value").BuildAsync();

        Assert.AreEqual("query{envVariables{value}}", query3);
    }
}
