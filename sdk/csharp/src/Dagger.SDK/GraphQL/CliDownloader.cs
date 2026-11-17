using System.Formats.Tar;
using System.IO;
using System.IO.Compression;
using System.Net;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using Dagger.Exceptions;

namespace Dagger.GraphQL;

/// <summary>
/// Thrown when the CLI release the SDK asks for is not published (yet), which is the normal
/// state of a development build of the SDK.
/// </summary>
internal sealed class CliReleaseUnavailableException(string message) : DaggerException(message);

/// <summary>
/// Downloads the Dagger CLI release that matches this SDK into the user's cache directory,
/// the way the other Dagger SDKs do: the archive is checked against the published checksum,
/// and CLIs of other versions are removed from the cache afterwards.
/// </summary>
internal sealed class CliDownloader
{
    internal const string BinPrefix = "dagger-";

    private const long MaxCliSize = 1024L * 1024 * 1024;

    private static readonly HttpClient DefaultHttp = new() { Timeout = Timeout.InfiniteTimeSpan };

    private readonly string _version;
    private readonly string _cacheDirectory;
    private readonly Uri _releases;
    private readonly HttpClient _http;

    public CliDownloader(
        string version,
        string? cacheDirectory = null,
        Uri? releases = null,
        HttpClient? http = null
    )
    {
        _version = version.TrimStart('v');
        _cacheDirectory = cacheDirectory ?? DefaultCacheDirectory();
        _releases = releases ?? new Uri("https://dl.dagger.io/dagger/releases/");
        _http = http ?? DefaultHttp;
    }

    internal string ArchiveName =>
        $"dagger_v{_version}_{OperatingSystemName()}_{ArchitectureName()}.{(OperatingSystem.IsWindows() ? "zip" : "tar.gz")}";

    /// <summary>Returns the path of the cached CLI, downloading it first when it is missing.</summary>
    public string Download()
    {
        if (OperatingSystem.IsWindows())
        {
            System.IO.Directory.CreateDirectory(_cacheDirectory);
        }
        else
        {
            System.IO.Directory.CreateDirectory(
                _cacheDirectory,
                UnixFileMode.UserRead | UnixFileMode.UserWrite | UnixFileMode.UserExecute
            );
        }

        var binName = BinPrefix + _version + (OperatingSystem.IsWindows() ? ".exe" : string.Empty);
        var binPath = Path.Combine(_cacheDirectory, binName);
        if (System.IO.File.Exists(binPath))
        {
            return binPath;
        }

        var expected = ExpectedChecksum();

        var unique = Guid.NewGuid().ToString("N");
        var archive = Path.Combine(_cacheDirectory, $"temp-{unique}-{ArchiveName}");
        var bin = Path.Combine(_cacheDirectory, $"temp-{unique}-{binName}");
        try
        {
            var actual = Fetch(ArchiveName, archive);
            if (!string.Equals(actual, expected, StringComparison.OrdinalIgnoreCase))
            {
                throw new DaggerException(
                    $"Checksum mismatch for {ArchiveName}: expected {expected}, got {actual}."
                );
            }

            Extract(archive, bin);
            if (!OperatingSystem.IsWindows())
            {
                System.IO.File.SetUnixFileMode(
                    bin,
                    UnixFileMode.UserRead | UnixFileMode.UserWrite | UnixFileMode.UserExecute
                );
            }

            try
            {
                System.IO.File.Move(bin, binPath);
            }
            catch (IOException) when (System.IO.File.Exists(binPath))
            {
                // Another process downloaded the same CLI in the meantime.
            }
        }
        finally
        {
            TryDelete(archive);
            TryDelete(bin);
        }

        RemoveOtherVersions(binName);
        return binPath;
    }

    internal static string DefaultCacheDirectory()
    {
        var xdg = Environment.GetEnvironmentVariable("XDG_CACHE_HOME");
        if (!string.IsNullOrWhiteSpace(xdg))
        {
            return Path.Combine(xdg, "dagger");
        }

        if (OperatingSystem.IsWindows())
        {
            return Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
                "cache",
                "dagger"
            );
        }

        var home = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
        return OperatingSystem.IsMacOS()
            ? Path.Combine(home, "Library", "Caches", "dagger")
            : Path.Combine(home, ".cache", "dagger");
    }

    private string ExpectedChecksum()
    {
        using var response = Get("checksums.txt");
        using var reader = new StreamReader(response.Content.ReadAsStream());
        while (reader.ReadLine() is { } line)
        {
            var parts = line.Split(' ', StringSplitOptions.RemoveEmptyEntries);
            if (parts.Length == 2 && parts[1] == ArchiveName)
            {
                return parts[0];
            }
        }

        throw new DaggerException($"No checksum for {ArchiveName} in {Address("checksums.txt")}.");
    }

    /// <summary>Downloads a release file and returns the SHA-256 of what was written.</summary>
    private string Fetch(string name, string destination)
    {
        using var response = Get(name);
        using var sha256 = IncrementalHash.CreateHash(HashAlgorithmName.SHA256);
        using (var source = response.Content.ReadAsStream())
        using (var target = System.IO.File.Create(destination))
        {
            var buffer = new byte[81920];
            int read;
            while ((read = source.Read(buffer, 0, buffer.Length)) > 0)
            {
                sha256.AppendData(buffer, 0, read);
                target.Write(buffer, 0, read);
            }
        }

        return Convert.ToHexString(sha256.GetHashAndReset()).ToLowerInvariant();
    }

    private HttpResponseMessage Get(string name)
    {
        var address = Address(name);
        HttpResponseMessage response;
        try
        {
            response = _http.Send(
                new HttpRequestMessage(HttpMethod.Get, address),
                HttpCompletionOption.ResponseHeadersRead
            );
        }
        catch (HttpRequestException exception)
        {
            throw new DaggerException(
                $"Failed to download {address}: {exception.Message}",
                exception
            );
        }

        if (response.IsSuccessStatusCode)
        {
            return response;
        }

        var status = response.StatusCode;
        response.Dispose();

        // dl.dagger.io answers 403 for a file that does not exist.
        if (status is HttpStatusCode.Forbidden or HttpStatusCode.NotFound)
        {
            throw new CliReleaseUnavailableException(
                $"Dagger CLI {_version} is not available at {address} (HTTP {(int)status})."
            );
        }

        throw new DaggerException($"Failed to download {address}: HTTP {(int)status}.");
    }

    private Uri Address(string name) => new(_releases, $"{_version}/{name}");

    private static void Extract(string archive, string destination)
    {
        if (OperatingSystem.IsWindows())
        {
            using var zip = ZipFile.OpenRead(archive);
            var entry =
                zip.Entries.FirstOrDefault(e => e.Name == "dagger.exe")
                ?? throw new DaggerException("The CLI archive does not contain dagger.exe.");
            using var source = entry.Open();
            Copy(source, destination);
            return;
        }

        using var file = System.IO.File.OpenRead(archive);
        using var gzip = new GZipStream(file, CompressionMode.Decompress);
        using var tar = new TarReader(gzip);
        while (tar.GetNextEntry() is { } entry)
        {
            if (Path.GetFileName(entry.Name) == "dagger" && entry.DataStream is not null)
            {
                Copy(entry.DataStream, destination);
                return;
            }
        }

        throw new DaggerException("The CLI archive does not contain the dagger binary.");
    }

    private static void Copy(Stream source, string destination)
    {
        using var target = System.IO.File.Create(destination);
        var buffer = new byte[81920];
        long total = 0;
        int read;
        while ((read = source.Read(buffer, 0, buffer.Length)) > 0)
        {
            total += read;
            if (total > MaxCliSize)
            {
                throw new DaggerException("The CLI in the downloaded archive is too large.");
            }

            target.Write(buffer, 0, read);
        }
    }

    private void RemoveOtherVersions(string current)
    {
        try
        {
            foreach (
                var path in System.IO.Directory.EnumerateFiles(_cacheDirectory, BinPrefix + "*")
            )
            {
                if (Path.GetFileName(path) != current)
                {
                    TryDelete(path);
                }
            }
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException)
        { }
    }

    private static void TryDelete(string path)
    {
        try
        {
            System.IO.File.Delete(path);
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException)
        { }
    }

    private static string OperatingSystemName()
    {
        if (OperatingSystem.IsWindows())
        {
            return "windows";
        }

        if (OperatingSystem.IsMacOS())
        {
            return "darwin";
        }

        if (OperatingSystem.IsLinux())
        {
            return "linux";
        }

        throw new DaggerException(
            $"There is no Dagger CLI release for {RuntimeInformation.OSDescription}. "
                + $"Set {CliSession.CliBinVariable} to a CLI you built or installed yourself."
        );
    }

    private static string ArchitectureName() =>
        RuntimeInformation.ProcessArchitecture switch
        {
            Architecture.X64 => "amd64",
            Architecture.Arm64 => "arm64",
            Architecture.Arm => "armv7",
            var other => throw new DaggerException(
                $"There is no Dagger CLI release for the {other} architecture. "
                    + $"Set {CliSession.CliBinVariable} to a CLI you built or installed yourself."
            ),
        };
}
