using Dagger.SDK.GraphQL;

namespace Dagger.SDK.Tests.GraphQL;

[TestClass]
public class QueryBuilderTest
{
    [TestMethod]
    public void TestSelect()
    {
        var query = QueryBuilder.Builder().Select("container").Build();

        Assert.AreEqual("query{container}", query);
    }

    [TestMethod]
    public void TestSelect_WithArgument()
    {
        var query = QueryBuilder
            .Builder()
            .Select("container")
            .Select("from", [new Argument("address", new StringValue("nginx"))])
            .Build();

        Assert.AreEqual("query{container{from(address:\"nginx\")}}", query);

        query = QueryBuilder
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
            .Build();

        Assert.AreEqual("query{container{withExec(args:[\"echo\",\"hello\"])}}", query);

        query = QueryBuilder
            .Builder()
            .Select(
                "buildDocker",
                [
                    new Argument(
                        "buildArgs",
                        new ObjectValue(
                            [KeyValuePair.Create("key", new StringValue("value") as Value)]
                        )
                    ),
                ]
            )
            .Build();

        Assert.AreEqual("query{buildDocker(buildArgs:{key:\"value\"})}", query);

        query = QueryBuilder
            .Builder()
            .Select("withEnvVariable", [new Argument("expand", new BooleanValue(true))])
            .Build();

        Assert.AreEqual("query{withEnvVariable(expand:true)}", query);
    }

    [TestMethod]
    public void TestSelect_ImmutableQuery()
    {
        var query1 = QueryBuilder.Builder().Select("envVariables");

        var query2 = query1.Select("name").Build();

        Assert.AreEqual("query{envVariables{name}}", query2);

        var query3 = query1.Select("value").Build();

        Assert.AreEqual("query{envVariables{value}}", query3);
    }

    [TestMethod]
    public void TestSelect_InlineFragment()
    {
        var query = QueryBuilder
            .Builder()
            .Select("node", [new Argument("id", new StringValue("abc"))])
            .InlineFragment("Container")
            .Select("stdout")
            .Build();

        Assert.AreEqual("query{node(id:\"abc\"){...on Container{stdout}}}", query);
    }

    [TestMethod]
    public void TestSelect_InlineFragment_Nested()
    {
        var query = QueryBuilder
            .Builder()
            .Select("node", [new Argument("id", new StringValue("abc"))])
            .InlineFragment("Container")
            .Select("envVariables")
            .Select("id")
            .Build();

        Assert.AreEqual("query{node(id:\"abc\"){...on Container{envVariables{id}}}}", query);
    }

    [TestMethod]
    public void TestNodeQueryBuilder()
    {
        var query = global::Dagger.SDK.Object
            .NodeQueryBuilder("some-id", "EnvVariable")
            .Select("name")
            .Build();

        Assert.AreEqual("query{node(id:\"some-id\"){...on EnvVariable{name}}}", query);
    }
}
