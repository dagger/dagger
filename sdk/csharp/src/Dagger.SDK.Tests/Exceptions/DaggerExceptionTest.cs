using System.Text.Json;
using Dagger.Exceptions;
using Dagger.GraphQL;

namespace Dagger.SDK.Tests.Exceptions;

[TestClass]
public class DaggerExceptionTest
{
    [TestMethod]
    public void TestQueryException_Construction()
    {
        var errors = new List<GraphQLError>
        {
            new GraphQLError
            {
                Message = "Test error",
                Path = new List<string> { "container", "from" }
            }
        };

        var ex = new QueryException("Test error", errors, "query{container{from}}");

        Assert.AreEqual("Test error", ex.Message);
        Assert.AreEqual(1, ex.Errors.Count);
        Assert.AreEqual("query{container{from}}", ex.Query);
    }

    [TestMethod]
    public void TestExecException_Construction()
    {
        var errors = new List<GraphQLError>
        {
            new GraphQLError
            {
                Message = "process exited with status 1",
                Extensions = new Dictionary<string, JsonElement>
                {
                    ["_type"] = JsonDocument.Parse("\"EXEC_ERROR\"").RootElement,
                    ["exitCode"] = JsonDocument.Parse("1").RootElement,
                    ["cmd"] = JsonDocument.Parse("[\"false\"]").RootElement,
                    ["stdout"] = JsonDocument.Parse("\"\"").RootElement,
                    ["stderr"] = JsonDocument.Parse("\"\"").RootElement
                }
            }
        };

        var ex = new ExecException(
            "process exited with status 1",
            errors,
            "query{container{withExec(args:[\"false\"])}}",
            new List<string> { "false" },
            1,
            "",
            ""
        );

        Assert.AreEqual("process exited with status 1", ex.Message);
        Assert.AreEqual(1, ex.ExitCode);
        Assert.AreEqual(1, ex.Command.Count);
        Assert.AreEqual("false", ex.Command[0]);
        Assert.AreEqual("", ex.Stdout);
        Assert.AreEqual("", ex.Stderr);
    }

    [TestMethod]
    public void TestGraphQLError_ErrorType()
    {
        var error = new GraphQLError
        {
            Message = "Test error",
            Extensions = new Dictionary<string, JsonElement>
            {
                ["_type"] = JsonDocument.Parse("\"EXEC_ERROR\"").RootElement
            }
        };

        Assert.AreEqual("EXEC_ERROR", error.ErrorType);
    }

    [TestMethod]
    public void TestGraphQLError_NoErrorType()
    {
        var error = new GraphQLError
        {
            Message = "Test error"
        };

        Assert.IsNull(error.ErrorType);
    }

    [TestMethod]
    public void TestExecException_ToString()
    {
        var errors = new List<GraphQLError>
        {
            new GraphQLError { Message = "process exited with status 1" }
        };

        var ex = new ExecException(
            "process exited with status 1",
            errors,
            "query{}",
            new List<string> { "sh", "-c", "exit 1" },
            1,
            "output",
            "error output"
        );

        var str = ex.ToString();
        Assert.IsTrue(str.Contains("process exited with status 1"));
        Assert.IsTrue(str.Contains("sh -c exit 1"));
        Assert.IsTrue(str.Contains("Exit Code: 1"));
        Assert.IsTrue(str.Contains("Stdout: output"));
        Assert.IsTrue(str.Contains("Stderr: error output"));
    }
}
