using System.Formats.Tar;
using System.IO.Compression;
using System.Net;
using System.Security.Cryptography;
using System.Text;
using Dagger.Exceptions;
using Dagger.GraphQL;

namespace Dagger.SDK.Tests.GraphQL;

[TestClass]
public class CliDownloaderTest
{
    private const string Version = "9.9.9";

    private string _cache = null!;

    [TestInitialize]
    public void CreateCacheDirectory()
    {
        _cache = Path.Combine(Path.GetTempPath(), "dagger-cache-" + Guid.NewGuid().ToString("N"));
        System.IO.Directory.CreateDirectory(_cache);
    }

    [TestCleanup]
    public void DeleteCacheDirectory() => System.IO.Directory.Delete(_cache, recursive: true);

    [TestMethod]
    public void Download_VerifiesTheArchiveAndCachesTheCli()
    {
        var releases = new Releases(Version, "the cli");
        var downloader = releases.Downloader(_cache);

        var cli = downloader.Download();

        Assert.AreEqual(_cache, Path.GetDirectoryName(cli));
        StringAssert.StartsWith(Path.GetFileName(cli), CliDownloader.BinPrefix + Version);
        Assert.AreEqual("the cli", System.IO.File.ReadAllText(cli));
        if (!OperatingSystem.IsWindows())
        {
            Assert.IsTrue(System.IO.File.GetUnixFileMode(cli).HasFlag(UnixFileMode.UserExecute));
        }

        CollectionAssert.AreEqual(
            new[] { Path.GetFileName(cli) },
            System.IO.Directory.EnumerateFiles(_cache).Select(Path.GetFileName).ToArray(),
            "only the CLI should be left in the cache"
        );

        var requests = releases.Requests;
        Assert.AreEqual(cli, downloader.Download());
        Assert.AreEqual(requests, releases.Requests, "a cached CLI should not be downloaded again");
    }

    [TestMethod]
    public void Download_RemovesClisOfOtherVersions()
    {
        var old = Path.Combine(_cache, CliDownloader.BinPrefix + "0.0.1");
        var unrelated = Path.Combine(_cache, "something-else");
        System.IO.File.WriteAllText(old, "old");
        System.IO.File.WriteAllText(unrelated, "keep");

        new Releases(Version, "the cli").Downloader(_cache).Download();

        Assert.IsFalse(System.IO.File.Exists(old));
        Assert.IsTrue(System.IO.File.Exists(unrelated));
    }

    [TestMethod]
    public void Download_WithAWrongChecksum_FailsAndLeavesNothingBehind()
    {
        var releases = new Releases(Version, "the cli") { Checksum = new string('0', 64) };

        var exception = Assert.ThrowsException<DaggerException>(() =>
            releases.Downloader(_cache).Download()
        );

        StringAssert.Contains(exception.Message, "Checksum mismatch");
        Assert.AreEqual(0, System.IO.Directory.EnumerateFiles(_cache).Count());
    }

    [TestMethod]
    [DataRow(HttpStatusCode.Forbidden)]
    [DataRow(HttpStatusCode.NotFound)]
    public void Download_WhenTheReleaseDoesNotExist_SaysSo(HttpStatusCode status)
    {
        var releases = new Releases(Version, "the cli") { Status = status };

        Assert.ThrowsException<CliReleaseUnavailableException>(() =>
            releases.Downloader(_cache).Download()
        );
    }

    [TestMethod]
    public void Download_WhenTheServerFails_IsNotTreatedAsAMissingRelease()
    {
        var releases = new Releases(Version, "the cli")
        {
            Status = HttpStatusCode.InternalServerError,
        };

        var exception = Assert.ThrowsException<DaggerException>(() =>
            releases.Downloader(_cache).Download()
        );

        Assert.IsNotInstanceOfType<CliReleaseUnavailableException>(exception);
    }

    [TestMethod]
    [DoNotParallelize]
    public void DefaultCacheDirectory_FollowsXdgCacheHome()
    {
        var saved = Environment.GetEnvironmentVariable("XDG_CACHE_HOME");
        Environment.SetEnvironmentVariable("XDG_CACHE_HOME", _cache);
        try
        {
            Assert.AreEqual(Path.Combine(_cache, "dagger"), CliDownloader.DefaultCacheDirectory());
        }
        finally
        {
            Environment.SetEnvironmentVariable("XDG_CACHE_HOME", saved);
        }
    }

    /// <summary>
    /// Stands in for dl.dagger.io: serves the checksums and the archive of one release,
    /// built for the platform the test runs on.
    /// </summary>
    internal sealed class Releases : HttpMessageHandler
    {
        private readonly string _version;
        private readonly byte[] _archive;

        public Releases(string version, string cli)
        {
            _version = version;
            _archive = Archive(Encoding.UTF8.GetBytes(cli));
            Checksum = Convert.ToHexString(SHA256.HashData(_archive)).ToLowerInvariant();
        }

        public string Checksum { get; init; }

        public HttpStatusCode Status { get; init; } = HttpStatusCode.OK;

        public int Requests { get; private set; }

        public CliDownloader Downloader(string cache) =>
            new(
                _version,
                cache,
                new Uri("http://releases.test/dagger/releases/"),
                new HttpClient(this)
            );

        protected override HttpResponseMessage Send(
            HttpRequestMessage request,
            CancellationToken cancellationToken
        )
        {
            Requests++;
            if (Status != HttpStatusCode.OK)
            {
                return new HttpResponseMessage(Status);
            }

            var downloader = new CliDownloader(_version);
            var path = request.RequestUri!.AbsolutePath;
            if (path == $"/dagger/releases/{_version}/checksums.txt")
            {
                return new HttpResponseMessage(HttpStatusCode.OK)
                {
                    Content = new StringContent(
                        $"{new string('1', 64)}  some_other_archive.tar.gz\n{Checksum}  {downloader.ArchiveName}\n"
                    ),
                };
            }

            if (path == $"/dagger/releases/{_version}/{downloader.ArchiveName}")
            {
                return new HttpResponseMessage(HttpStatusCode.OK)
                {
                    Content = new ByteArrayContent(_archive),
                };
            }

            return new HttpResponseMessage(HttpStatusCode.NotFound);
        }

        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken
        ) => Task.FromResult(Send(request, cancellationToken));

        private static byte[] Archive(byte[] cli)
        {
            using var archive = new MemoryStream();
            if (OperatingSystem.IsWindows())
            {
                using (var zip = new ZipArchive(archive, ZipArchiveMode.Create, leaveOpen: true))
                {
                    using var entry = zip.CreateEntry("dagger.exe").Open();
                    entry.Write(cli);
                }

                return archive.ToArray();
            }

            using (var gzip = new GZipStream(archive, CompressionMode.Compress, leaveOpen: true))
            using (var tar = new TarWriter(gzip))
            {
                tar.WriteEntry(
                    new PaxTarEntry(TarEntryType.RegularFile, "LICENSE")
                    {
                        DataStream = new MemoryStream(Encoding.UTF8.GetBytes("license")),
                    }
                );
                tar.WriteEntry(
                    new PaxTarEntry(TarEntryType.RegularFile, "dagger")
                    {
                        DataStream = new MemoryStream(cli),
                    }
                );
            }

            return archive.ToArray();
        }
    }
}
