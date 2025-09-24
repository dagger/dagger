using System.Collections;

namespace Dagger.SDK.Tests;

[TestClass]
public class ClientTest
{
    private static readonly Query _dag = Dagger.Dag();

    [TestMethod]
    public async Task TestSimple()
    {
        var output = await _dag.Container()
            .From("debian")
            .WithExec(["echo", "hello"])
            .StdoutAsync();

        Assert.AreEqual("hello\n", output, ignoreCase: true);
    }

    [TestMethod]
    public async Task TestCancellation()
    {
        var cts = new CancellationTokenSource();
        cts.CancelAfter(5000);
        await Assert.ThrowsExceptionAsync<TaskCanceledException>(
            () =>
                _dag.Container()
                    .From("debian")
                    .WithExec(["bash", "-c", "sleep 10; echo hello"])
                    .StdoutAsync(cts.Token)
        );
    }

    [TestMethod]
    public async Task TestOptionalArguments()
    {
        var env = await _dag.Container()
            .From("debian")
            .WithEnvVariable("A", "a")
            .WithEnvVariable("B", "b")
            .WithEnvVariable("C", "$A:$B", expand: true)
            .EnvVariableAsync("C");

        Assert.AreEqual("a:b", env, ignoreCase: false);
    }

    [TestMethod]
    public async Task TestScalarIdSerialization()
    {
        var cache = _dag.CacheVolume("hello");
        var id = await cache.IdAsync();
        Assert.IsTrue(id.Value.Length > 0);
    }

    [TestMethod]
    public async Task TestInputObject()
    {
        const string dockerfile = """
            FROM alpine:3.20.0
            ARG SPAM=spam
            ENV SPAM=$SPAM
            CMD printenv
            """;

        var dockerDir = _dag.Directory().WithNewFile("Dockerfile", dockerfile);
        var output = await dockerDir
            .DockerBuild(buildArgs: [new BuildArg("SPAM", "egg")])
            .WithExec([])
            .StdoutAsync();

        StringAssert.Contains(output, "SPAM=egg");
    }

    [TestMethod]
    public async Task TestStringEscape()
    {
        await _dag.Container()
            .From("alpine")
            .WithNewFile(
                "/a.txt",
                contents: """
                  \\  /       Partly cloudy
                _ /\"\".-.     +29(31) °C
                  \\_(   ).   ↑ 13 km/h
                  /(___(__)  10 km
                             0.0 mm
                """
            )
            .SyncAsync();
    }

    [TestMethod]
    public async Task TestReturnArray()
    {
        var envs = await _dag.Container()
            .WithEnvVariable("A", "B")
            .WithEnvVariable("C", "D")
            .EnvVariablesAsync();

        ICollection envNames = envs.Select(env => env.NameAsync())
            .Select(task => task.Result)
            .ToList();
        ICollection expected = new[] { "A", "C" };
        CollectionAssert.AreEqual(expected, envNames);
    }
}
