using System.Net;
using System.Net.Sockets;
using System.Text;
using System.Text.Json;
using Dagger.Exceptions;
using Dagger.GraphQL;

namespace Dagger.SDK.Tests;

[TestClass]
public class QueryExecutorErrorTest
{
    [TestMethod]
    public void TestGraphQLResponse_PathWithListIndex()
    {
        var response = JsonSerializer.Deserialize<GraphQLResponse>(
            """{"errors":[{"message":"boom","path":["container","envVariables",0,"value"]}]}"""
        );

        var path = response!.Errors![0].Path!;
        Assert.AreEqual("boom", response.Errors[0].Message);
        Assert.AreEqual("envVariables", path[1].GetString());
        Assert.AreEqual(0, path[2].GetInt32());
    }

    [TestMethod]
    public async Task TestExecute_NonJsonHttpError()
    {
        var port = FreePort();
        using var listener = new HttpListener();
        listener.Prefixes.Add($"http://localhost:{port}/");
        listener.Start();
        var serve = Task.Run(async () =>
        {
            var ctx = await listener.GetContextAsync();
            ctx.Response.StatusCode = 401;
            var body = Encoding.UTF8.GetBytes("invalid session token\n");
            await ctx.Response.OutputStream.WriteAsync(body);
            ctx.Response.Close();
        });

        using var client = new GraphQLClient(port.ToString(), "token");
        var ex = await Assert.ThrowsExceptionAsync<DaggerException>(() =>
            QueryExecutor.ExecuteAsync<string>(client, QueryBuilder.Builder().Select("version"))
        );
        await serve;

        StringAssert.Contains(ex.Message, "401");
        StringAssert.Contains(ex.Message, "invalid session token");
    }

    private static int FreePort()
    {
        using var socket = new TcpListener(IPAddress.Loopback, 0);
        socket.Start();
        return ((IPEndPoint)socket.LocalEndpoint).Port;
    }
}
