using Dagger.SDK.GraphQL;

namespace Dagger.SDK.Tests;

[TestClass]
public class QueryExecutorTest
{
    [TestMethod]
    public async Task TestExecute()
    {
        var gqlClient = new GraphQLClient();
        var queryBuilder = QueryBuilder
            .Builder()
            .Select("container")
            .Select("from", [new Argument("address", new StringValue("alpine"))])
            .Select("id");

        string id = await SDK.QueryExecutor.ExecuteAsync<string>(gqlClient, queryBuilder);

        Assert.IsFalse(string.IsNullOrWhiteSpace(id));
    }

    [TestMethod]
    public async Task TestExecuteList()
    {
        var gqlClient = new GraphQLClient();
        var queryBuilder = QueryBuilder
            .Builder()
            .Select("container")
            .Select("from", [new Argument("address", new StringValue("alpine"))])
            .Select("envVariables")
            .Select("id");

        var ids = await SDK.QueryExecutor.ExecuteListAsync<EnvVariableId>(gqlClient, queryBuilder);

        Assert.IsTrue(ids.Length > 0);
        CollectionAssert.AllItemsAreNotNull(ids);

        Console.WriteLine(ids);
    }
}
