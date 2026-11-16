using System.Diagnostics;
using Dagger.Exceptions;
using Dagger.GraphQL;

namespace Dagger.SDK.Tests.GraphQL;

/// <summary>
/// These tests change process-wide environment variables, so they must not run in parallel
/// with each other.
/// </summary>
[TestClass]
[DoNotParallelize]
public class CliSessionTest
{
    private readonly Dictionary<string, string?> _saved = new();

    [TestInitialize]
    public void ClearSessionEnvironment()
    {
        foreach (
            var name in new[]
            {
                GraphQLClient.SessionPortVariable,
                GraphQLClient.SessionTokenVariable,
                CliSession.CliBinVariable,
            }
        )
        {
            _saved[name] = Environment.GetEnvironmentVariable(name);
            Environment.SetEnvironmentVariable(name, null);
        }
    }

    [TestCleanup]
    public void RestoreSessionEnvironment()
    {
        foreach (var (name, value) in _saved)
        {
            Environment.SetEnvironmentVariable(name, value);
        }
    }

    [TestMethod]
    public void Start_ReadsPortAndTokenAndEndsTheCliByClosingItsInput()
    {
        if (OperatingSystem.IsWindows())
        {
            Assert.Inconclusive("The stand-in CLI is a shell script.");
        }

        var arguments = Path.GetTempFileName();
        var stopped = Path.Combine(Path.GetTempPath(), "stopped-" + Guid.NewGuid().ToString("N"));
        var cli = StandInCli(
            $$"""
            echo "$@" > '{{arguments}}'
            echo '{"port":4321,"session_token":"secret"}'
            cat > /dev/null
            echo stopped > '{{stopped}}'
            """
        );
        Environment.SetEnvironmentVariable(CliSession.CliBinVariable, cli);

        try
        {
            using (var session = CliSession.Start())
            {
                Assert.AreEqual("4321", session.Port);
                Assert.AreEqual("secret", session.Token);
                Assert.IsFalse(System.IO.File.Exists(stopped));
            }

            Assert.IsTrue(
                System.IO.File.Exists(stopped),
                "the CLI should end by itself when its input is closed, not by being killed"
            );
            Assert.AreEqual(
                "session --label dagger.io/sdk.name:csharp --label dagger.io/sdk.version:"
                    + $"{SdkVersion()} --version {CliVersion.Value}",
                System.IO.File.ReadAllText(arguments).Trim()
            );
        }
        finally
        {
            System.IO.File.Delete(cli);
            System.IO.File.Delete(arguments);
            System.IO.File.Delete(stopped);
        }
    }

    [TestMethod]
    [DataRow("123")]
    [DataRow("""{"port":"4321","session_token":"secret"}""")]
    [DataRow("""{"port":4321,"session_token":null}""")]
    [DataRow("""{"port":4321}""")]
    public void Start_WhenTheFirstLineIsNotASession_FailsAndEndsTheCli(string firstLine)
    {
        if (OperatingSystem.IsWindows())
        {
            Assert.Inconclusive("The stand-in CLI is a shell script.");
        }

        var pidFile = Path.GetTempFileName();
        var cli = StandInCli(
            $$"""
            echo $$ > '{{pidFile}}'
            echo '{{firstLine}}'
            sleep 60
            """
        );
        Environment.SetEnvironmentVariable(CliSession.CliBinVariable, cli);

        try
        {
            var exception = Assert.ThrowsException<DaggerException>(() => CliSession.Start());

            StringAssert.Contains(exception.Message, firstLine);
            var pid = int.Parse(System.IO.File.ReadAllText(pidFile).Trim());
            Assert.IsTrue(HasExited(pid), "the CLI should not be left running");
        }
        finally
        {
            System.IO.File.Delete(cli);
            System.IO.File.Delete(pidFile);
        }
    }

    [TestMethod]
    public void Start_WithoutACli_SaysWhatIsMissing()
    {
        Environment.SetEnvironmentVariable(
            CliSession.CliBinVariable,
            Path.Combine(Path.GetTempPath(), "no-such-dagger-" + Guid.NewGuid().ToString("N"))
        );

        var exception = Assert.ThrowsException<DaggerException>(() => CliSession.Start());

        StringAssert.Contains(exception.Message, GraphQLClient.SessionPortVariable);
        StringAssert.Contains(exception.Message, CliSession.CliBinVariable);
    }

    [TestMethod]
    public void Start_WhenTheCliPrintsSomethingElse_Fails()
    {
        if (OperatingSystem.IsWindows())
        {
            Assert.Inconclusive("The stand-in CLI is a shell script.");
        }

        var cli = StandInCli("echo 'not json'\ncat > /dev/null");
        Environment.SetEnvironmentVariable(CliSession.CliBinVariable, cli);

        try
        {
            var exception = Assert.ThrowsException<DaggerException>(() => CliSession.Start());
            StringAssert.Contains(exception.Message, "not json");
        }
        finally
        {
            System.IO.File.Delete(cli);
        }
    }

    [TestMethod]
    public void Start_WhenTheCliExitsEarly_ReportsItsOutput()
    {
        if (OperatingSystem.IsWindows())
        {
            Assert.Inconclusive("The stand-in CLI is a shell script.");
        }

        var cli = StandInCli("echo 'no engine' >&2\nexit 3");
        Environment.SetEnvironmentVariable(CliSession.CliBinVariable, cli);

        try
        {
            var exception = Assert.ThrowsException<DaggerException>(() => CliSession.Start());
            StringAssert.Contains(exception.Message, "code 3");
            StringAssert.Contains(exception.Message, "no engine");
        }
        finally
        {
            System.IO.File.Delete(cli);
        }
    }

    [TestMethod]
    public void Start_WithoutAConfiguredCli_DownloadsTheCliAndRunsIt()
    {
        if (OperatingSystem.IsWindows())
        {
            Assert.Inconclusive("The stand-in CLI is a shell script.");
        }

        var cache = System.IO.Directory.CreateTempSubdirectory("dagger-cache-").FullName;
        var releases = new CliDownloaderTest.Releases(
            "9.9.9",
            "#!/bin/sh\necho '{\"port\":4321,\"session_token\":\"downloaded\"}'\ncat > /dev/null\n"
        );

        try
        {
            using var session = CliSession.Start(releases.Downloader(cache));

            Assert.AreEqual("4321", session.Port);
            Assert.AreEqual("downloaded", session.Token);
        }
        finally
        {
            System.IO.Directory.Delete(cache, recursive: true);
        }
    }

    [TestMethod]
    public void Start_WhenTheReleaseDoesNotExistAndNoCliIsInstalled_SaysBoth()
    {
        var cache = System.IO.Directory.CreateTempSubdirectory("dagger-cache-").FullName;
        var releases = new CliDownloaderTest.Releases("9.9.9", "unused")
        {
            Status = System.Net.HttpStatusCode.Forbidden,
        };
        var path = Environment.GetEnvironmentVariable("PATH");
        Environment.SetEnvironmentVariable("PATH", cache);

        try
        {
            var exception = Assert.ThrowsException<DaggerException>(() =>
                CliSession.Start(releases.Downloader(cache))
            );

            StringAssert.Contains(exception.Message, "9.9.9 is not available");
            StringAssert.Contains(exception.Message, "no dagger on the PATH");
            StringAssert.Contains(exception.Message, CliSession.CliBinVariable);
        }
        finally
        {
            Environment.SetEnvironmentVariable("PATH", path);
            System.IO.Directory.Delete(cache, recursive: true);
        }
    }

    [TestMethod]
    public void Start_WhenTheReleaseDoesNotExist_UsesTheCliFromThePath()
    {
        if (OperatingSystem.IsWindows())
        {
            Assert.Inconclusive("The stand-in CLI is a shell script.");
        }

        var directory = System.IO.Directory.CreateTempSubdirectory("dagger-path-").FullName;
        var installed = Path.Combine(directory, "dagger");
        System.IO.File.Move(
            StandInCli("echo '{\"port\":4321,\"session_token\":\"installed\"}'\ncat > /dev/null"),
            installed
        );
        var releases = new CliDownloaderTest.Releases("9.9.9", "unused")
        {
            Status = System.Net.HttpStatusCode.Forbidden,
        };
        var path = Environment.GetEnvironmentVariable("PATH");
        Environment.SetEnvironmentVariable(
            "PATH",
            directory + Path.PathSeparator + "/usr/bin" + Path.PathSeparator + "/bin"
        );

        try
        {
            Assert.AreEqual(installed, CliSession.FindOnPath("dagger"));

            using var session = CliSession.Start(releases.Downloader(directory));

            Assert.AreEqual("installed", session.Token);
        }
        finally
        {
            Environment.SetEnvironmentVariable("PATH", path);
            System.IO.Directory.Delete(directory, recursive: true);
        }
    }

    [TestMethod]
    public void FindOnPath_IgnoresTheCurrentDirectory()
    {
        var directory = System.IO.Directory.CreateTempSubdirectory("dagger-cwd-").FullName;
        System.IO.File.WriteAllText(Path.Combine(directory, "dagger"), "not on the PATH");
        System.IO.File.WriteAllText(Path.Combine(directory, "dagger.exe"), "not on the PATH");
        var path = Environment.GetEnvironmentVariable("PATH");
        var current = Environment.CurrentDirectory;
        Environment.SetEnvironmentVariable("PATH", Path.GetTempPath() + "no-such-directory");
        Environment.CurrentDirectory = directory;

        try
        {
            Assert.IsNull(CliSession.FindOnPath("dagger"));
        }
        finally
        {
            Environment.CurrentDirectory = current;
            Environment.SetEnvironmentVariable("PATH", path);
            System.IO.Directory.Delete(directory, recursive: true);
        }
    }

    [TestMethod]
    public void Client_WithAPortButNoToken_FailsInsteadOfStartingAnotherSession()
    {
        Environment.SetEnvironmentVariable(GraphQLClient.SessionPortVariable, "1234");
        Environment.SetEnvironmentVariable(
            CliSession.CliBinVariable,
            Path.Combine(Path.GetTempPath(), "no-such-dagger-" + Guid.NewGuid().ToString("N"))
        );

        var exception = Assert.ThrowsException<DaggerException>(() => new GraphQLClient());

        StringAssert.Contains(exception.Message, GraphQLClient.SessionTokenVariable);
    }

    [TestMethod]
    public void Client_UsesTheProvidedSessionWithoutStartingACli()
    {
        Environment.SetEnvironmentVariable(GraphQLClient.SessionPortVariable, "1");
        Environment.SetEnvironmentVariable(GraphQLClient.SessionTokenVariable, "token");
        Environment.SetEnvironmentVariable(
            CliSession.CliBinVariable,
            Path.Combine(Path.GetTempPath(), "no-such-dagger-" + Guid.NewGuid().ToString("N"))
        );

        using var client = new GraphQLClient();
    }

    private static string StandInCli(string body)
    {
        var path = Path.Combine(
            Path.GetTempPath(),
            "dagger-stand-in-" + Guid.NewGuid().ToString("N")
        );
        System.IO.File.WriteAllText(path, "#!/bin/sh\n" + body.ReplaceLineEndings("\n") + "\n");
        if (!OperatingSystem.IsWindows())
        {
            System.IO.File.SetUnixFileMode(
                path,
                UnixFileMode.UserRead | UnixFileMode.UserWrite | UnixFileMode.UserExecute
            );
        }

        return path;
    }

    private static string SdkVersion() =>
        typeof(CliSession)
            .Assembly.GetCustomAttributes(
                typeof(System.Reflection.AssemblyInformationalVersionAttribute),
                false
            )
            .Cast<System.Reflection.AssemblyInformationalVersionAttribute>()
            .Single()
            .InformationalVersion.Split('+')[0];

    private static bool HasExited(int pid)
    {
        for (var attempt = 0; attempt < 50; attempt++)
        {
            try
            {
                using var process = Process.GetProcessById(pid);
                if (process.HasExited)
                {
                    return true;
                }
            }
            catch (ArgumentException)
            {
                return true;
            }

            Thread.Sleep(100);
        }

        return false;
    }
}
