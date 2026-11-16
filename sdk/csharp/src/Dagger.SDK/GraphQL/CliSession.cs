using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using System.Reflection;
using System.Text;
using System.Text.Json;
using Dagger.Exceptions;

namespace Dagger.GraphQL;

/// <summary>
/// An engine session started through the <c>dagger</c> CLI, for programs that are not run
/// under <c>dagger run</c> or inside a Dagger container (a test runner in an IDE, for example).
/// The session lives as long as this object: disposing it, or the end of the process, closes
/// the CLI's stdin, which ends the session and everything started through it.
/// </summary>
internal sealed class CliSession : IDisposable
{
    internal const string CliBinVariable = "_EXPERIMENTAL_DAGGER_CLI_BIN";

    private const int TextFileBusy = 26;

    private static readonly TimeSpan StartTimeout = TimeSpan.FromMinutes(5);
    private static readonly TimeSpan StopTimeout = TimeSpan.FromMinutes(5);

    private readonly Process _process;
    private int _disposed;

    private CliSession(Process process, string port, string token)
    {
        _process = process;
        Port = port;
        Token = token;
        AppDomain.CurrentDomain.ProcessExit += OnProcessExit;
    }

    public string Port { get; }

    public string Token { get; }

    /// <summary>
    /// Starts <c>dagger session</c> and waits for it to report its port and token.
    /// The CLI is the one named by <c>_EXPERIMENTAL_DAGGER_CLI_BIN</c> when that is set.
    /// Otherwise it is the release this SDK is built for, downloaded on first use, or
    /// <c>dagger</c> from the PATH when that release is not published.
    /// </summary>
    public static CliSession Start() => Start(new CliDownloader(CliVersion.Value));

    internal static CliSession Start(CliDownloader downloader)
    {
        var configured = Environment.GetEnvironmentVariable(CliBinVariable);
        if (!string.IsNullOrEmpty(configured))
        {
            return Launch(ExpandHome(configured));
        }

        string cli;
        try
        {
            cli = downloader.Download();
        }
        catch (CliReleaseUnavailableException unavailable)
        {
            var installed =
                FindOnPath("dagger")
                ?? throw new DaggerException(
                    $"{NoSession} and {unavailable.Message[..^1]}, and there is no dagger on the PATH "
                        + $"to use instead. Install the CLI, set {CliBinVariable}, or run the program "
                        + "under `dagger run`."
                );
            Console.Error.WriteLine(
                $"{unavailable.Message} Using {installed} from the PATH; version compatibility is not guaranteed."
            );
            return Launch(installed);
        }
        catch (Exception exception)
            when (exception is DaggerException or IOException or UnauthorizedAccessException)
        {
            throw new DaggerException(
                $"{NoSession} and Dagger CLI {CliVersion.Value} could not be downloaded: "
                    + $"{exception.Message} Set {CliBinVariable} to use a CLI that is already installed.",
                exception
            );
        }

        return Launch(cli);
    }

    private static string NoSession =>
        $"No Dagger session is available: {GraphQLClient.SessionPortVariable} and "
        + $"{GraphQLClient.SessionTokenVariable} are not set,";

    private static CliSession Launch(string cli)
    {
        var stderr = new StringBuilder();
        var process = StartProcess(cli, stderr);

        string? firstLine;
        try
        {
            var reading = process.StandardOutput.ReadLineAsync();
            if (!reading.Wait(StartTimeout))
            {
                throw Failed(
                    process,
                    stderr,
                    $"`{cli} session` did not report a session within {StartTimeout.TotalSeconds:F0} s."
                );
            }

            firstLine = reading.Result;
        }
        catch (AggregateException exception)
        {
            throw Failed(
                process,
                stderr,
                $"`{cli} session` could not be read: {exception.InnerException?.Message}"
            );
        }

        if (firstLine is null)
        {
            process.WaitForExit();
            throw Failed(
                process,
                stderr,
                $"`{cli} session` exited with code {process.ExitCode} before it reported a session."
            );
        }

        string port;
        string? token;
        try
        {
            using var connect = JsonDocument.Parse(firstLine);
            port = connect.RootElement.GetProperty("port").GetInt32().ToString();
            token = connect.RootElement.GetProperty("session_token").GetString();
        }
        catch (Exception exception)
            when (exception
                    is JsonException
                        or KeyNotFoundException
                        or InvalidOperationException
                        or FormatException
            )
        {
            token = null;
            port = string.Empty;
        }

        if (string.IsNullOrEmpty(token))
        {
            throw Failed(
                process,
                stderr,
                $"`{cli} session` printed an unexpected first line: {firstLine}"
            );
        }

        _ = process
            .StandardOutput.BaseStream.CopyToAsync(Stream.Null)
            .ContinueWith(static copy => _ = copy.Exception, TaskContinuationOptions.OnlyOnFaulted);

        return new CliSession(process, port, token);
    }

    /// <summary>Ends the CLI and returns the exception to throw, with what the CLI wrote to stderr.</summary>
    private static DaggerException Failed(Process process, StringBuilder stderr, string message)
    {
        Kill(process);
        string output;
        lock (stderr)
        {
            output = stderr.ToString();
        }

        process.Dispose();
        return new DaggerException(
            message + (output.Length > 0 ? Environment.NewLine + output : string.Empty)
        );
    }

    private static Process StartProcess(string cli, StringBuilder stderr)
    {
        for (var attempt = 0; ; attempt++)
        {
            var startInfo = new ProcessStartInfo(cli)
            {
                RedirectStandardInput = true,
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                UseShellExecute = false,
            };
            startInfo.ArgumentList.Add("session");
            startInfo.ArgumentList.Add("--label");
            startInfo.ArgumentList.Add("dagger.io/sdk.name:csharp");
            startInfo.ArgumentList.Add("--label");
            startInfo.ArgumentList.Add($"dagger.io/sdk.version:{SdkVersion()}");
            startInfo.ArgumentList.Add("--version");
            startInfo.ArgumentList.Add(CliVersion.Value);
            if (Activity.Current is { IdFormat: ActivityIdFormat.W3C, Id: { } traceParent })
            {
                startInfo.Environment["TRACEPARENT"] = traceParent;
            }

            var process = new Process { StartInfo = startInfo };
            process.ErrorDataReceived += (_, line) =>
            {
                if (line.Data is null)
                {
                    return;
                }

                lock (stderr)
                {
                    if (stderr.Length < 8192)
                    {
                        stderr.AppendLine(line.Data);
                    }
                }
            };

            try
            {
                process.Start();
            }
            catch (Win32Exception exception)
                when (exception.NativeErrorCode == TextFileBusy && attempt < 10)
            {
                // The CLI was just written by a download in another process.
                process.Dispose();
                Thread.Sleep(100);
                continue;
            }
            catch (Exception exception)
                when (exception is Win32Exception or InvalidOperationException)
            {
                process.Dispose();
                throw new DaggerException(
                    $"{NoSession} and the Dagger CLI could not be started ({cli}): {exception.Message}. "
                        + $"Install the CLI, set {CliBinVariable}, or run the program under `dagger run`.",
                    exception
                );
            }

            process.BeginErrorReadLine();
            return process;
        }
    }

    public void Dispose()
    {
        if (Interlocked.Exchange(ref _disposed, 1) != 0)
        {
            return;
        }

        AppDomain.CurrentDomain.ProcessExit -= OnProcessExit;

        try
        {
            _process.StandardInput.Close();
            if (!_process.WaitForExit(StopTimeout))
            {
                Kill(_process);
            }
        }
        catch (Exception exception) when (exception is InvalidOperationException or IOException)
        {
            Kill(_process);
        }

        _process.Dispose();
    }

    private void OnProcessExit(object? sender, EventArgs e) => Dispose();

    private static void Kill(Process process)
    {
        try
        {
            process.Kill(entireProcessTree: true);
        }
        catch (Exception exception) when (exception is InvalidOperationException or Win32Exception)
        {
            // Already gone.
        }
    }

    private static string ExpandHome(string path) =>
        path == "~"
        || path.StartsWith("~/", StringComparison.Ordinal)
        || path.StartsWith(@"~\", StringComparison.Ordinal)
            ? Environment.GetFolderPath(Environment.SpecialFolder.UserProfile) + path[1..]
            : path;

    /// <summary>
    /// Finds a program in the directories of the PATH only, as a shell would. Starting it by
    /// name instead would also look next to the running program and in the current directory.
    /// </summary>
    internal static string? FindOnPath(string name)
    {
        string[] extensions = OperatingSystem.IsWindows()
            ? (Environment.GetEnvironmentVariable("PATHEXT") ?? ".COM;.EXE;.BAT;.CMD").Split(
                ';',
                StringSplitOptions.RemoveEmptyEntries
            )
            : [string.Empty];

        foreach (
            var directory in (Environment.GetEnvironmentVariable("PATH") ?? string.Empty).Split(
                Path.PathSeparator,
                StringSplitOptions.RemoveEmptyEntries
            )
        )
        {
            foreach (var extension in extensions)
            {
                var candidate = Path.Combine(directory, name + extension);
                if (!System.IO.File.Exists(candidate))
                {
                    continue;
                }

                const UnixFileMode executable =
                    UnixFileMode.UserExecute
                    | UnixFileMode.GroupExecute
                    | UnixFileMode.OtherExecute;
                if (
                    OperatingSystem.IsWindows()
                    || (System.IO.File.GetUnixFileMode(candidate) & executable) != 0
                )
                {
                    return Path.GetFullPath(candidate);
                }
            }
        }

        return null;
    }

    private static string SdkVersion() =>
        typeof(CliSession)
            .Assembly.GetCustomAttribute<AssemblyInformationalVersionAttribute>()
            ?.InformationalVersion.Split('+')[0]
        ?? "n/a";
}
