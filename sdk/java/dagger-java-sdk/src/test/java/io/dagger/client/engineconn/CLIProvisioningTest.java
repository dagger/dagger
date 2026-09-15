package io.dagger.client.engineconn;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.catchThrowableOfType;
import static org.junit.jupiter.api.Assertions.assertTimeoutPreemptively;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.never;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

import com.ongres.process.FluentProcess;
import com.sun.net.httpserver.HttpServer;
import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.InetSocketAddress;
import java.net.URL;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.junit.jupiter.api.io.TempDir;
import uk.org.webcompere.systemstubs.environment.EnvironmentVariables;
import uk.org.webcompere.systemstubs.jupiter.SystemStub;
import uk.org.webcompere.systemstubs.jupiter.SystemStubsExtension;
import uk.org.webcompere.systemstubs.stream.SystemErr;

@ExtendWith(SystemStubsExtension.class)
class CLIProvisioningTest {

  @SystemStub private EnvironmentVariables environmentVariables;
  @SystemStub private SystemErr systemErr;
  @TempDir private Path tempDir;

  @BeforeEach
  void clearCLIOverride() {
    environmentVariables.set("_EXPERIMENTAL_DAGGER_CLI_BIN", null);
  }

  @Test
  void checksumMarksReleaseUnavailable() {
    for (int statusCode :
        new int[] {HttpURLConnection.HTTP_FORBIDDEN, HttpURLConnection.HTTP_NOT_FOUND}) {
      CLIDownloader downloader = new CLIDownloader(fetcherReturning(statusCode));

      IOException error =
          catchThrowableOfType(() -> downloader.fetchChecksumMap("unreleased"), IOException.class);

      assertThat(error)
          .isInstanceOf(CLIReleaseUnavailableException.class)
          .hasMessageContaining(Integer.toString(statusCode));
    }
  }

  @Test
  void followsRedirectAndClassifiesFinalChecksumStatus() throws Exception {
    AtomicInteger finalStatus = new AtomicInteger();
    AtomicInteger redirectRequests = new AtomicInteger();
    AtomicInteger checksumRequests = new AtomicInteger();
    HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    server.createContext(
        "/redirect",
        exchange -> {
          redirectRequests.incrementAndGet();
          exchange.getResponseHeaders().add("Location", "/checksums");
          exchange.sendResponseHeaders(HttpURLConnection.HTTP_MOVED_TEMP, -1);
          exchange.close();
        });
    server.createContext(
        "/checksums",
        exchange -> {
          checksumRequests.incrementAndGet();
          exchange.sendResponseHeaders(finalStatus.get(), -1);
          exchange.close();
        });
    server.start();

    try {
      String redirectURL =
          String.format("http://127.0.0.1:%d/redirect", server.getAddress().getPort());
      for (int statusCode :
          new int[] {HttpURLConnection.HTTP_FORBIDDEN, HttpURLConnection.HTTP_NOT_FOUND}) {
        finalStatus.set(statusCode);
        CLIDownloader downloader = new CLIDownloader(ignored -> FileFetcher.fetchURL(redirectURL));

        IOException error =
            catchThrowableOfType(
                () -> downloader.fetchChecksumMap("unreleased"), IOException.class);

        assertThat(error)
            .isInstanceOf(CLIReleaseUnavailableException.class)
            .hasMessageContaining(Integer.toString(statusCode));
      }
      assertThat(redirectRequests).hasValue(2);
      assertThat(checksumRequests).hasValue(2);
    } finally {
      server.stop(0);
    }
  }

  @Test
  void responseCloseClosesBodyAndDisconnects() throws Exception {
    TrackingHttpURLConnection connection =
        new TrackingHttpURLConnection(HttpURLConnection.HTTP_OK, null);

    try (FileFetcher.Response response = FileFetcher.fetchConnection(connection)) {
      assertThat(response.statusCode()).isEqualTo(HttpURLConnection.HTTP_OK);
    }

    assertThat(connection.body.closed).isTrue();
    assertThat(connection.disconnected).isTrue();
  }

  @Test
  void responseSetupFailureDisconnects() throws Exception {
    IOException setupError = new IOException("response failed");
    TrackingHttpURLConnection connection = new TrackingHttpURLConnection(0, setupError);

    IOException error =
        catchThrowableOfType(() -> FileFetcher.fetchConnection(connection), IOException.class);

    assertThat(error).isSameAs(setupError);
    assertThat(connection.disconnected).isTrue();
  }

  @Test
  void otherChecksumErrorsDoNotMarkReleaseUnavailable() {
    CLIDownloader downloader =
        new CLIDownloader(fetcherReturning(HttpURLConnection.HTTP_INTERNAL_ERROR));

    IOException error =
        catchThrowableOfType(() -> downloader.fetchChecksumMap("unreleased"), IOException.class);

    assertThat(error)
        .isNotInstanceOf(CLIReleaseUnavailableException.class)
        .hasMessageContaining("500");
  }

  @Test
  void unavailableChecksumStopsBeforeArchiveDownload() {
    List<String> fetchedURLs = new ArrayList<>();
    String version = "unreleased-" + UUID.randomUUID();
    CLIDownloader downloader =
        new CLIDownloader(
            url -> {
              fetchedURLs.add(url);
              return response(HttpURLConnection.HTTP_NOT_FOUND);
            });

    IOException error =
        catchThrowableOfType(() -> downloader.downloadCLI(version), IOException.class);

    assertThat(error).isInstanceOf(CLIReleaseUnavailableException.class);
    assertThat(fetchedURLs)
        .containsExactly(
            String.format("https://dl.dagger.io/dagger/releases/%s/checksums.txt", version));
  }

  @Test
  void missingArchiveDoesNotMarkReleaseUnavailable() {
    // Missing checksums mean the release is absent; a missing archive may be a
    // partial or broken release, so it must remain fatal.
    for (int statusCode :
        new int[] {HttpURLConnection.HTTP_FORBIDDEN, HttpURLConnection.HTTP_NOT_FOUND}) {
      CLIDownloader downloader = new CLIDownloader(fetcherReturning(statusCode));

      IOException error =
          catchThrowableOfType(
              () ->
                  downloader.extractCLI("archive.tar.gz", "unreleased", tempDir.resolve("dagger")),
              IOException.class);

      assertThat(error)
          .isNotInstanceOf(CLIReleaseUnavailableException.class)
          .hasMessageContaining(Integer.toString(statusCode));
    }
  }

  @Test
  void usesDaggerCLIInPathAndWarns() throws Exception {
    String binName = isWindows() ? "dagger.exe" : "dagger";
    Path binPath = Files.createFile(tempDir.resolve(binName));
    assertThat(binPath.toFile().setExecutable(true)).isTrue();
    environmentVariables.set("PATH", tempDir.toString());
    if (isWindows()) {
      environmentVariables.set("PATHEXT", ".EXE");
    }

    CLIReleaseUnavailableException downloadError =
        new CLIReleaseUnavailableException("download failed");
    CLIDownloader downloader = mock(CLIDownloader.class);
    when(downloader.downloadCLI()).thenThrow(downloadError);
    CLIRunner runner = new CLIRunner(".", false, downloader);

    String actual = runner.getCLIPath();

    assertThat(actual).isEqualTo(binPath.toString());
    assertThat(systemErr.getText())
        .startsWith(
            String.format(
                    "CLI version %s is unavailable; using %s from PATH (version compatibility is not guaranteed).",
                    Provisioning.getCLIVersion(), binPath)
                + System.lineSeparator());
  }

  @Test
  void unrelatedDownloadErrorDoesNotFallback() throws Exception {
    IOException downloadError = new IOException("download failed");
    CLIDownloader downloader = mock(CLIDownloader.class);
    when(downloader.downloadCLI()).thenThrow(downloadError);
    CLIPathResolver resolver = mock(CLIPathResolver.class);
    CLIRunner runner = runner(downloader, resolver, new ArrayList<>());

    IOException error = catchThrowableOfType(runner::getCLIPath, IOException.class);

    assertThat(error).isSameAs(downloadError);
    verify(resolver, never()).findDaggerCLI();
  }

  @Test
  void preservesDownloadAndPathErrorsWhenNoCLIIsFound() throws Exception {
    environmentVariables.set("PATH", tempDir.toString());
    CLIReleaseUnavailableException downloadError =
        new CLIReleaseUnavailableException("download failed");
    CLIDownloader downloader = mock(CLIDownloader.class);
    when(downloader.downloadCLI()).thenThrow(downloadError);
    CLIRunner runner = runner(downloader, new CLIPathResolver(), new ArrayList<>());

    IOException error = catchThrowableOfType(runner::getCLIPath, IOException.class);

    assertThat(error)
        .hasMessageContaining("download failed")
        .hasMessageContaining("dagger executable was not found");
    assertThat(error.getCause()).isSameAs(downloadError);
    assertThat(error.getSuppressed()).hasSize(1);
    assertThat(error.getSuppressed()[0])
        .isInstanceOf(IOException.class)
        .hasMessage("dagger executable was not found");
  }

  @Test
  void preservesDownloadAndSessionErrorsWhenFallbackHandshakeFails() throws Exception {
    CLIReleaseUnavailableException downloadError =
        new CLIReleaseUnavailableException("download failed");
    RuntimeException sessionError = new RuntimeException("session failed");
    CLIDownloader downloader = mock(CLIDownloader.class);
    CLIPathResolver resolver = mock(CLIPathResolver.class);
    when(resolver.findDaggerCLI()).thenReturn("/usr/local/bin/dagger");
    CLIRunner runner =
        new CLIRunner(".", false, downloader, resolver, warning -> {}) {
          @Override
          void streamSessionOutput() {
            throw sessionError;
          }
        };
    runner.fallbackToLocalCLI(downloadError);

    RuntimeException runError = catchThrowableOfType(runner::run, RuntimeException.class);

    IOException error = catchThrowableOfType(runner::getConnectionParams, IOException.class);

    assertThat(runError).isSameAs(sessionError);
    assertThat(error)
        .hasMessageContaining("download failed")
        .hasMessageContaining("session failed");
    assertThat(error.getCause()).isSameAs(downloadError);
    assertThat(error.getSuppressed()).containsExactly(sessionError);
  }

  @Test
  void preservesDownloadErrorWhenFallbackEndsBeforeHandshake() throws Exception {
    CLIReleaseUnavailableException downloadError =
        new CLIReleaseUnavailableException("download failed");
    CLIDownloader downloader = mock(CLIDownloader.class);
    CLIPathResolver resolver = mock(CLIPathResolver.class);
    when(resolver.findDaggerCLI()).thenReturn("/usr/local/bin/dagger");
    CLIRunner runner =
        new CLIRunner(".", false, downloader, resolver, warning -> {}) {
          @Override
          void streamSessionOutput() {}
        };
    runner.fallbackToLocalCLI(downloadError);

    runner.run();

    IOException error =
        assertTimeoutPreemptively(
            Duration.ofSeconds(1),
            () -> catchThrowableOfType(runner::getConnectionParams, IOException.class));

    assertThat(error)
        .hasMessageContaining("download failed")
        .hasMessageContaining("CLI session ended before connection parameters were received");
    assertThat(error.getCause()).isSameAs(downloadError);
    assertThat(error.getSuppressed())
        .singleElement()
        .isInstanceOf(IOException.class)
        .hasMessage("CLI session ended before connection parameters were received");
  }

  @Test
  void preservesDownloadErrorWhenFallbackStreamClosesBeforeHandshake() throws Exception {
    CLIReleaseUnavailableException downloadError =
        new CLIReleaseUnavailableException("download failed");
    CLIDownloader downloader = mock(CLIDownloader.class);
    CLIPathResolver resolver = mock(CLIPathResolver.class);
    when(resolver.findDaggerCLI()).thenReturn("/usr/local/bin/dagger");
    CLIRunner runner =
        new CLIRunner(".", false, downloader, resolver, warning -> {}) {
          @Override
          void streamSessionOutput() {
            throw new RuntimeException(new IOException("Stream closed"));
          }
        };
    runner.fallbackToLocalCLI(downloadError);

    runner.run();

    IOException error =
        assertTimeoutPreemptively(
            Duration.ofSeconds(1),
            () -> catchThrowableOfType(runner::getConnectionParams, IOException.class));

    assertThat(error)
        .hasMessageContaining("download failed")
        .hasMessageContaining("CLI session ended before connection parameters were received");
    assertThat(error.getCause()).isSameAs(downloadError);
    assertThat(error.getSuppressed())
        .singleElement()
        .isInstanceOf(IOException.class)
        .hasMessage("CLI session ended before connection parameters were received");
  }

  @Test
  void preservesDownloadAndSessionErrorsWhenFallbackProcessStartFails() throws Exception {
    CLIReleaseUnavailableException downloadError =
        new CLIReleaseUnavailableException("download failed");
    IOException sessionError = new IOException("session failed");
    CLIDownloader downloader = mock(CLIDownloader.class);
    when(downloader.downloadCLI()).thenThrow(downloadError);
    CLIPathResolver resolver = mock(CLIPathResolver.class);
    when(resolver.findDaggerCLI()).thenReturn("/usr/local/bin/dagger");
    CLIRunner runner =
        new CLIRunner(".", false, downloader, resolver, warning -> {}) {
          @Override
          FluentProcess startProcess(List<String> command) throws IOException {
            throw sessionError;
          }
        };

    IOException error = catchThrowableOfType(runner::start, IOException.class);

    assertThat(error)
        .hasMessageContaining("download failed")
        .hasMessageContaining("session failed");
    assertThat(error.getCause()).isSameAs(downloadError);
    assertThat(error.getSuppressed()).containsExactly(sessionError);
  }

  @Test
  void explicitCLIOverrideKeepsPriority() throws Exception {
    environmentVariables.set("_EXPERIMENTAL_DAGGER_CLI_BIN", "/explicit/dagger");
    CLIDownloader downloader = mock(CLIDownloader.class);
    CLIRunner runner = runner(downloader, new CLIPathResolver(), new ArrayList<>());

    assertThat(runner.getCLIPath()).isEqualTo("/explicit/dagger");
    verify(downloader, never()).downloadCLI();
  }

  @Test
  void normalizesWindowsPathAndPathExt() {
    List<String> candidates =
        new CLIPathResolver()
            .daggerCLIPathCandidates(true, "C:\\bin;;\"D:\\other bin\"", "EXE;;.CMD;");

    assertThat(candidates)
        .containsExactly(
            "C:\\bin\\dagger.exe",
            "C:\\bin\\dagger.cmd",
            "D:\\other bin\\dagger.exe",
            "D:\\other bin\\dagger.cmd");
  }

  @Test
  void usesDefaultWindowsExecutableExtensionsWhenPathExtIsEmpty() {
    List<String> candidates = new CLIPathResolver().daggerCLIPathCandidates(true, "C:\\bin", "");

    assertThat(candidates)
        .containsExactly(
            "C:\\bin\\dagger.com",
            "C:\\bin\\dagger.exe",
            "C:\\bin\\dagger.bat",
            "C:\\bin\\dagger.cmd");
  }

  private FileFetcher fetcherReturning(int statusCode) {
    return url -> response(statusCode);
  }

  private FileFetcher.Response response(int statusCode) {
    return new FileFetcher.Response(
        statusCode, "test status", new ByteArrayInputStream(new byte[0]));
  }

  private CLIRunner runner(
      CLIDownloader downloader, CLIPathResolver resolver, List<String> warnings) {
    return new CLIRunner(".", false, downloader, resolver, warnings::add);
  }

  private boolean isWindows() {
    return System.getProperty("os.name").toLowerCase().contains("win");
  }

  private static class TrackingHttpURLConnection extends HttpURLConnection {
    private final int responseCode;
    private final IOException responseError;
    private final TrackingInputStream body = new TrackingInputStream();
    private boolean disconnected;

    TrackingHttpURLConnection(int responseCode, IOException responseError) throws Exception {
      super(new URL("http://example.test"));
      this.responseCode = responseCode;
      this.responseError = responseError;
    }

    @Override
    public int getResponseCode() throws IOException {
      if (responseError != null) {
        throw responseError;
      }
      return responseCode;
    }

    @Override
    public String getResponseMessage() {
      return "test status";
    }

    @Override
    public InputStream getInputStream() {
      return body;
    }

    @Override
    public InputStream getErrorStream() {
      return body;
    }

    @Override
    public void disconnect() {
      disconnected = true;
    }

    @Override
    public boolean usingProxy() {
      return false;
    }

    @Override
    public void connect() {}
  }

  private static class TrackingInputStream extends ByteArrayInputStream {
    private boolean closed;

    TrackingInputStream() {
      super(new byte[0]);
    }

    @Override
    public void close() throws IOException {
      closed = true;
      super.close();
    }
  }
}
