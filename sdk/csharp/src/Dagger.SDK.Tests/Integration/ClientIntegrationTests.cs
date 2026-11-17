using static Dagger.Client;

namespace Dagger.SDK.Tests.Integration;

/// <summary>
/// Integration tests for the Dagger.SDK client library usage pattern.
/// These tests verify the SDK works correctly when used as a standalone client
/// (not as a Dagger module runtime).
///
/// Note: These tests require the Dagger engine to be running and will make
/// actual API calls. They use the generated SDK code from introspection.json.
/// </summary>
[TestClass]
public class ClientIntegrationTests
{
    [TestMethod]
    [TestCategory("Integration")]
    public async Task TestSimpleContainerExecution()
    {
        var output = await Dag.Container()
            .From("alpine:latest")
            .WithExec(["echo", "hello world"])
            .Stdout();

        Assert.AreEqual("hello world\n", output);
    }

    [TestMethod]
    [TestCategory("Integration")]
    public async Task TestContainerWithMultipleExec()
    {
        var output = await Dag.Container()
            .From("alpine:latest")
            .WithExec(["sh", "-c", "echo foo && echo bar"])
            .Stdout();

        Assert.AreEqual("foo\nbar\n", output);
    }

    [TestMethod]
    [TestCategory("Integration")]
    public async Task TestContainerEnvironmentVariables()
    {
        var output = await Dag.Container()
            .From("alpine:latest")
            .WithEnvVariable("TEST_VAR", "test_value")
            .WithExec(["sh", "-c", "echo $TEST_VAR"])
            .Stdout();

        Assert.AreEqual("test_value\n", output);
    }

    [TestMethod]
    [TestCategory("Integration")]
    public async Task TestDirectoryOperations()
    {
        var dir = Dag.Directory().WithNewFile("hello.txt", "Hello from Dagger SDK!");

        var fileContents = await Dag.Container()
            .From("alpine:latest")
            .WithMountedDirectory("/data", dir)
            .WithExec(["cat", "/data/hello.txt"])
            .Stdout();

        Assert.AreEqual("Hello from Dagger SDK!", fileContents);
    }

    [TestMethod]
    [TestCategory("Integration")]
    public async Task TestGitRepository()
    {
        var readme = await Dag.Git("https://github.com/dagger/dagger")
            .Tag("v0.3.0")
            .Tree()
            .File("README.md")
            .Contents();

        Assert.IsTrue(readme.Contains("What is Dagger?"));
    }

    [TestMethod]
    [TestCategory("Integration")]
    public async Task TestContainerBuild()
    {
        var dockerfile =
            @"FROM alpine:latest
RUN echo 'Hello from Dockerfile'
";
        var dir = Dag.Directory().WithNewFile("Dockerfile", dockerfile);

        var container = dir.DockerBuild();
        var id = await container.Id();

        Assert.IsFalse(string.IsNullOrWhiteSpace(id.ToString()));
    }

    [TestMethod]
    [TestCategory("Integration")]
    public async Task TestDirectoryEntries()
    {
        var dir = Dag.Directory()
            .WithNewFile("hello.txt", "Hello, world!")
            .WithNewFile("goodbye.txt", "Goodbye, world!");

        var entries = await dir.Entries();

        Assert.AreEqual(2, entries.Length);
        CollectionAssert.Contains(entries, "hello.txt");
        CollectionAssert.Contains(entries, "goodbye.txt");
    }

    [TestMethod]
    [TestCategory("Integration")]
    public async Task TestContainerEnvVariables()
    {
        var container = Dag.Container()
            .From("alpine:3.16.2")
            .WithEnvVariable("FOO", "BAR")
            .WithEnvVariable("BAZ", "QUX");

        var envs = await container.EnvVariables();

        Assert.IsTrue(envs.Length >= 2); // At least our 2 vars, plus PATH from base image

        // Find our custom env vars
        var fooVar = envs.FirstOrDefault(e => e.Name().Result == "FOO");
        var bazVar = envs.FirstOrDefault(e => e.Name().Result == "BAZ");

        Assert.IsNotNull(fooVar);
        Assert.AreEqual("BAR", await fooVar.Value());

        Assert.IsNotNull(bazVar);
        Assert.AreEqual("QUX", await bazVar.Value());
    }

    [TestMethod]
    [TestCategory("Integration")]
    public async Task TestContainerWithWorkdir()
    {
        var output = await Dag.Container()
            .From("alpine:latest")
            .WithWorkdir("/tmp")
            .WithExec(["pwd"])
            .Stdout();

        Assert.AreEqual("/tmp\n", output);
    }

    [TestMethod]
    [TestCategory("Integration")]
    public async Task TestContainerWithUser()
    {
        var output = await Dag.Container()
            .From("alpine:latest")
            .WithUser("nobody")
            .WithExec(["whoami"])
            .Stdout();

        Assert.AreEqual("nobody\n", output);
    }

    [TestMethod]
    [TestCategory("Integration")]
    public async Task TestChainingOperations()
    {
        var result = await Dag.Container()
            .From("alpine:latest")
            .WithEnvVariable("GREETING", "Hello")
            .WithEnvVariable("NAME", "Dagger")
            .WithExec(["sh", "-c", "echo $GREETING $NAME"])
            .Stdout();

        Assert.AreEqual("Hello Dagger\n", result);
    }

    [TestMethod]
    [TestCategory("Integration")]
    public async Task TestFileOperations()
    {
        var content = "test file content\n";
        var file = Dag.Directory().WithNewFile("test.txt", content).File("test.txt");

        var retrieved = await file.Contents();

        Assert.AreEqual(content, retrieved);
    }

    [TestMethod]
    [TestCategory("Integration")]
    public async Task TestContainerExport()
    {
        var container = Dag.Container().From("alpine:latest").WithExec(["echo", "test"]);

        // Get container ID to verify it exists
        var id = await container.Id();

        Assert.IsFalse(string.IsNullOrWhiteSpace(id.ToString()));
    }

    [TestMethod]
    [TestCategory("Integration")]
    public async Task TestCacheVolume()
    {
        // First run - populate cache
        await Dag.Container()
            .From("alpine:latest")
            .WithMountedCache("/cache", Dag.CacheVolume("test-cache"))
            .WithExec(["sh", "-c", "echo 'cached data' > /cache/data.txt"])
            .Sync();

        // Second run - read from cache
        var output = await Dag.Container()
            .From("alpine:latest")
            .WithMountedCache("/cache", Dag.CacheVolume("test-cache"))
            .WithExec(["cat", "/cache/data.txt"])
            .Stdout();

        Assert.AreEqual("cached data\n", output);
    }

    [TestMethod]
    [TestCategory("Integration")]
    public async Task TestSecret()
    {
        var secret = Dag.SetSecret("test-secret", "secret-value");

        var output = await Dag.Container()
            .From("alpine:latest")
            .WithSecretVariable("SECRET", secret)
            .WithExec(["sh", "-c", "echo $SECRET"])
            .Stdout();

        // Secrets are redacted in output for security
        Assert.AreEqual("***\n", output);
    }
}
