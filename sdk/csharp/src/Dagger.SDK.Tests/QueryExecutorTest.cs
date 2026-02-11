using Dagger.GraphQL;

namespace Dagger.SDK.Tests;

[TestClass]
public class QueryExecutorTest
{
    [TestMethod]
    public async Task TestExecute()
    {
        using var gqlClient = new GraphQLClient();
        var queryBuilder = QueryBuilder
            .Builder()
            .Select("container")
            .Select("from", [new Argument("address", new StringValue("alpine"))])
            .Select("id");

        string id = await QueryExecutor.ExecuteAsync<string>(gqlClient, queryBuilder);

        Assert.IsFalse(string.IsNullOrWhiteSpace(id));
    }

    [TestMethod]
    public async Task TestExecuteList()
    {
        using var gqlClient = new GraphQLClient();
        var queryBuilder = QueryBuilder
            .Builder()
            .Select("container")
            .Select("from", [new Argument("address", new StringValue("alpine"))])
            .Select("envVariables")
            .Select("id");

        var ids = await QueryExecutor.ExecuteListAsync<EnvVariableId>(gqlClient, queryBuilder);

        Assert.IsTrue(ids.Length > 0);
        CollectionAssert.AllItemsAreNotNull(ids);
    }

    [TestMethod]
    public async Task TestExecuteListOfStrings()
    {
        using var gqlClient = new GraphQLClient();
        var queryBuilder = QueryBuilder
            .Builder()
            .Select("directory")
            .Select(
                "withNewFile",
                [
                    new Argument("path", new StringValue("/test.txt")),
                    new Argument("contents", new StringValue("hello")),
                ]
            )
            .Select("entries");

        var entries = await QueryExecutor.ExecuteListAsync<string>(gqlClient, queryBuilder);

        Assert.IsTrue(entries.Length > 0);
        Assert.IsTrue(entries.Contains("test.txt"));
    }
}
