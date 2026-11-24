package io.dagger.client.engineconn;

import io.dagger.client.modules.core.Version;
import jakarta.json.Json;
import jakarta.json.JsonObject;
import jakarta.json.JsonReader;
import java.io.BufferedReader;
import java.io.File;
import java.io.IOException;
import java.io.InputStreamReader;
import java.io.StringReader;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import java.util.concurrent.atomic.AtomicBoolean;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

/**
 * A {@code dagger session} this process started, for code that runs with no session in its
 * environment: a standalone client, a test, an application.
 *
 * <p>The CLI comes from {@code _EXPERIMENTAL_DAGGER_CLI_BIN} or, failing that, {@code dagger} on
 * the {@code PATH}. Nothing is downloaded: this uses whatever Dagger the host has and says so
 * clearly when there is none. Provisioning has its own release, checksum and mirror questions and
 * is deliberately not part of this SDK.
 */
final class CLISession implements AutoCloseable {

  private static final Logger LOG = LoggerFactory.getLogger(CLISession.class);

  private static final Duration HANDSHAKE_TIMEOUT = Duration.ofMinutes(5);

  // A cache export packs its layers while the session shuts down, so the CLI gets as long to leave
  // on its own as sdk/go/engineconn/session.go in dagger/dagger gives it. Killing it earlier
  // truncates the export.
  private static final Duration SHUTDOWN_GRACE = Duration.ofMinutes(5);

  private static final Duration FORCED_EXIT_TIMEOUT = Duration.ofSeconds(10);

  private static final Duration STDERR_DRAIN_TIMEOUT = Duration.ofSeconds(5);

  private final Process process;
  private final int port;
  private final String sessionToken;
  private final Duration shutdownGrace;
  private final AtomicBoolean closed = new AtomicBoolean();
  private volatile Thread shutdownHook;

  private CLISession(Process process, int port, String sessionToken, Duration shutdownGrace) {
    this.process = process;
    this.port = port;
    this.sessionToken = sessionToken;
    this.shutdownGrace = shutdownGrace;
  }

  static CLISession start(Path workingDir, boolean loadWorkspaceModules) throws IOException {
    Path cli = resolveCLI(System.getenv("_EXPERIMENTAL_DAGGER_CLI_BIN"), System.getenv("PATH"));
    return start(cli, workingDir, loadWorkspaceModules, HANDSHAKE_TIMEOUT, SHUTDOWN_GRACE);
  }

  static CLISession start(
      Path cli,
      Path workingDir,
      boolean loadWorkspaceModules,
      Duration handshakeTimeout,
      Duration shutdownGrace)
      throws IOException {
    List<String> command = new ArrayList<>(List.of(cli.toString(), "session"));
    if (!Version.VERSION.isBlank()) {
      command.add("--version");
      command.add(Version.VERSION);
    }
    if (loadWorkspaceModules) {
      command.add("--load-workspace-modules");
    }
    LOG.debug("opening a dagger session: {}", command);

    Process process = new ProcessBuilder(command).directory(workingDir.toFile()).start();
    Stderr stderr = Stderr.pumping(process);
    CompletableFuture<String> announcement = readStdout(process);

    String line;
    try {
      line = announcement.get(handshakeTimeout.toMillis(), TimeUnit.MILLISECONDS);
    } catch (TimeoutException e) {
      throw failed(
          cli,
          process,
          stderr,
          "said nothing within " + handshakeTimeout.toSeconds() + "s",
          e,
          shutdownGrace);
    } catch (ExecutionException e) {
      throw failed(cli, process, stderr, "could not be read from", e, shutdownGrace);
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      throw failed(cli, process, stderr, "was interrupted while starting", e, shutdownGrace);
    }
    if (line == null) {
      throw failed(
          cli, process, stderr, "exited with code " + exitCode(process), null, shutdownGrace);
    }
    return announced(cli, process, stderr, line, shutdownGrace);
  }

  static Path resolveCLI(String configured, String searchPath) throws IOException {
    if (configured != null && !configured.isBlank() && Files.isExecutable(Path.of(configured))) {
      return Path.of(configured);
    }
    for (String dir : (searchPath == null ? "" : searchPath).split(File.pathSeparator)) {
      Path candidate = Path.of(dir).resolve("dagger");
      if (Files.isExecutable(candidate)) {
        return candidate;
      }
    }
    throw new IOException(
        "no Dagger session in the environment (DAGGER_SESSION_PORT and DAGGER_SESSION_TOKEN) and no"
            + " dagger CLI to start one: point _EXPERIMENTAL_DAGGER_CLI_BIN at a dagger binary, or"
            + " put dagger on the PATH. This SDK does not download one.");
  }

  int port() {
    return port;
  }

  String sessionToken() {
    return sessionToken;
  }

  boolean isAlive() {
    return process.isAlive();
  }

  int exitValue() {
    return process.exitValue();
  }

  /**
   * Stop the session. Idempotent, and also run at JVM exit so a session never outlives its owner.
   */
  @Override
  public void close() {
    if (!closed.compareAndSet(false, true)) {
      return;
    }
    removeShutdownHook();
    stop(process, shutdownGrace);
  }

  private static CLISession announced(
      Path cli, Process process, Stderr stderr, String line, Duration shutdownGrace)
      throws IOException {
    int port;
    String sessionToken;
    try (JsonReader reader = Json.createReader(new StringReader(line))) {
      JsonObject params = reader.readObject();
      port = params.getInt("port");
      sessionToken = params.getString("session_token");
    } catch (RuntimeException e) {
      throw failed(
          cli, process, stderr, "announced a line this SDK cannot read: " + line, e, shutdownGrace);
    }
    if (port < 1 || port > 65535) {
      throw failed(
          cli,
          process,
          stderr,
          "announced " + port + ", which is not a TCP port",
          null,
          shutdownGrace);
    }
    if (sessionToken.isEmpty()) {
      throw failed(cli, process, stderr, "announced an empty session token", null, shutdownGrace);
    }

    CLISession session = new CLISession(process, port, sessionToken, shutdownGrace);
    // Past the handshake the rest of standard error is progress, for the user rather than for a
    // failure message.
    stderr.stopCapturing();
    session.shutdownHook = new Thread(session::close, "dagger-session-shutdown");
    Runtime.getRuntime().addShutdownHook(session.shutdownHook);
    return session;
  }

  /**
   * The first line the session writes, plus a drain of everything after it: an unread pipe would
   * eventually block the CLI.
   */
  private static CompletableFuture<String> readStdout(Process process) {
    CompletableFuture<String> announcement = new CompletableFuture<>();
    Thread reader =
        new Thread(
            () -> {
              try (BufferedReader stdout =
                  new BufferedReader(
                      new InputStreamReader(process.getInputStream(), StandardCharsets.UTF_8))) {
                String line = stdout.readLine();
                announcement.complete(line);
                while ((line = stdout.readLine()) != null) {
                  LOG.debug(line);
                }
              } catch (IOException e) {
                announcement.completeExceptionally(e);
              } finally {
                announcement.complete(null);
              }
            },
            "dagger-session-stdout");
    reader.setDaemon(true);
    reader.start();
    return announcement;
  }

  /**
   * The real reason a session failed to start is on its standard error, so every failure carries
   * it. The process is stopped first: it has no owner yet, and its stream has to reach its end
   * before there is anything to quote.
   */
  private static IOException failed(
      Path cli,
      Process process,
      Stderr stderr,
      String what,
      Throwable cause,
      Duration shutdownGrace) {
    stop(process, shutdownGrace);
    return new IOException(
        "`" + cli + " session` " + what + ", and wrote to standard error:\n" + stderr.captured(),
        cause);
  }

  private static int exitCode(Process process) {
    try {
      return process.waitFor();
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      return -1;
    }
  }

  // The CLI shuts a session down cleanly when its standard input closes; killing it outright would
  // leave the engine to notice on its own.
  private static void stop(Process process, Duration grace) {
    try {
      process.getOutputStream().close();
    } catch (IOException alreadyGone) {
      // the process is no longer reachable, so there is nothing left to ask nicely
    }
    try {
      if (process.waitFor(grace.toMillis(), TimeUnit.MILLISECONDS)) {
        return;
      }
      LOG.warn("the dagger session did not exit within {}s; killing it", grace.toSeconds());
      process.destroyForcibly().waitFor(FORCED_EXIT_TIMEOUT.toMillis(), TimeUnit.MILLISECONDS);
    } catch (InterruptedException e) {
      process.destroyForcibly();
      Thread.currentThread().interrupt();
    }
  }

  private void removeShutdownHook() {
    Thread hook = shutdownHook;
    if (hook == null) {
      return;
    }
    shutdownHook = null;
    try {
      Runtime.getRuntime().removeShutdownHook(hook);
    } catch (IllegalStateException shuttingDown) {
      // close() is running from the hook itself, or alongside it
    }
  }

  /** Forwards the session's standard error to the log, and keeps its start for diagnostics. */
  private static final class Stderr {

    private static final int CAPTURE_LIMIT = 8192;

    private final StringBuilder captured = new StringBuilder();
    private volatile Thread pump;
    private volatile boolean capturing = true;

    static Stderr pumping(Process process) {
      Stderr stderr = new Stderr();
      stderr.pump = new Thread(() -> stderr.forward(process), "dagger-session-stderr");
      stderr.pump.setDaemon(true);
      stderr.pump.start();
      return stderr;
    }

    void stopCapturing() {
      capturing = false;
    }

    String captured() {
      try {
        pump.join(STDERR_DRAIN_TIMEOUT.toMillis());
      } catch (InterruptedException e) {
        Thread.currentThread().interrupt();
      }
      synchronized (captured) {
        return captured.toString();
      }
    }

    private void forward(Process process) {
      try (BufferedReader stderr =
          new BufferedReader(
              new InputStreamReader(process.getErrorStream(), StandardCharsets.UTF_8))) {
        String line;
        while ((line = stderr.readLine()) != null) {
          LOG.info(line);
          capture(line);
        }
      } catch (IOException gone) {
        // the process is gone, and with it anything it had left to say
      }
    }

    private void capture(String line) {
      if (!capturing) {
        return;
      }
      synchronized (captured) {
        if (captured.length() < CAPTURE_LIMIT) {
          captured.append(line).append(System.lineSeparator());
        }
      }
    }
  }
}
