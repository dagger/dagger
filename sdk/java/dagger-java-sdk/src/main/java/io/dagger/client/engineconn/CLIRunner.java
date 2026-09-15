package io.dagger.client.engineconn;

import com.ongres.process.FluentProcess;
import jakarta.json.Json;
import jakarta.json.JsonObject;
import jakarta.json.JsonReader;
import java.io.IOException;
import java.io.StringReader;
import java.util.List;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.function.Consumer;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

class CLIRunner implements Runnable {

  static final Logger LOG = LoggerFactory.getLogger(CLIRunner.class);

  private final String workingDir;
  private final boolean loadWorkspaceModules;
  private FluentProcess process;
  private ConnectParams params;
  private boolean failed = false;
  private ExecutorService executorService;
  private final CLIDownloader cliDownloader;
  private final CLIPathResolver cliPathResolver;
  private final Consumer<String> warningOutput;
  private IOException downloadError;
  private String fallbackCLIPath;
  private Throwable sessionError;

  public CLIRunner(String workingDir, boolean loadWorkspaceModules, CLIDownloader cliDownloader)
      throws IOException {
    this(
        workingDir,
        loadWorkspaceModules,
        cliDownloader,
        new CLIPathResolver(),
        System.err::println);
  }

  CLIRunner(
      String workingDir,
      boolean loadWorkspaceModules,
      CLIDownloader cliDownloader,
      CLIPathResolver cliPathResolver,
      Consumer<String> warningOutput) {
    this.workingDir = workingDir;
    this.loadWorkspaceModules = loadWorkspaceModules;
    this.cliDownloader = cliDownloader;
    this.cliPathResolver = cliPathResolver;
    this.warningOutput = warningOutput;
  }

  String getCLIPath() throws IOException {
    String cliBinPath = System.getenv("_EXPERIMENTAL_DAGGER_CLI_BIN");
    if (cliBinPath == null) {
      try {
        cliBinPath = cliDownloader.downloadCLI();
      } catch (IOException error) {
        cliBinPath = fallbackToLocalCLI(error);
      }
    }
    LOG.info("Found dagger CLI: " + cliBinPath);
    return cliBinPath;
  }

  String fallbackToLocalCLI(IOException error) throws IOException {
    if (!(error instanceof CLIReleaseUnavailableException)) {
      throw error;
    }

    String cliBinPath;
    try {
      cliBinPath = cliPathResolver.findDaggerCLI();
    } catch (IOException pathError) {
      throw combineErrors(
          error, "dagger CLI not found in PATH: " + pathError.getMessage(), pathError);
    }

    downloadError = error;
    fallbackCLIPath = cliBinPath;
    warningOutput.accept(
        String.format(
            "CLI version %s is unavailable; using %s from PATH (version compatibility is not guaranteed).",
            Provisioning.getCLIVersion(), cliBinPath));
    return cliBinPath;
  }

  synchronized ConnectParams getConnectionParams() throws IOException {
    while (params == null) {
      try {
        if (failed) {
          if (downloadError != null) {
            Throwable error =
                sessionError == null
                    ? new IOException("Could not connect to Dagger engine")
                    : sessionError;
            throw fallbackSessionError(fallbackCLIPath, error);
          }
          throw new IOException("Could not connect to Dagger engine");
        }
        wait();
      } catch (InterruptedException e) {
      }
    }
    return params;
  }

  synchronized void setFailed(Throwable error) {
    this.sessionError = error;
    this.failed = true;
    notifyAll();
  }

  synchronized void setParams(ConnectParams params) {
    this.params = params;
    notifyAll();
  }

  public void start() throws IOException {
    var command =
        new java.util.ArrayList<String>(
            java.util.List.of(
                getCLIPath(),
                "session",
                "--workdir",
                workingDir,
                "--label",
                "dagger.io/sdk.name:java",
                "--label",
                "dagger.io/sdk.version:" + Provisioning.getSDKVersion()));
    if (loadWorkspaceModules) {
      command.add("--load-workspace-modules");
    }
    try {
      this.process = startProcess(command);
    } catch (IOException sessionError) {
      if (downloadError != null) {
        throw fallbackSessionError(command.get(0), sessionError);
      }
      throw sessionError;
    } catch (RuntimeException sessionError) {
      if (downloadError != null) {
        throw fallbackSessionError(command.get(0), sessionError);
      }
      throw sessionError;
    }
    LOG.debug("Opening session: {}", process.toString());
    executorService = Executors.newSingleThreadExecutor(r -> new Thread(r, "dagger-runner"));
    executorService.execute(this);
  }

  FluentProcess startProcess(List<String> command) throws IOException {
    return FluentProcess.start(
            command.get(0), command.subList(1, command.size()).toArray(new String[0]))
        .withAllowedExitCodes(137);
  }

  @Override
  public void run() {
    try {
      streamSessionOutput();
    } catch (RuntimeException e) {
      if (!(e.getCause() instanceof IOException
          && "Stream closed".equals(e.getCause().getMessage()))) {
        LOG.error(e.getMessage(), e);
        setFailed(e);
        throw e;
      }
    } finally {
      setFailedIfNoParams(
          new IOException("CLI session ended before connection parameters were received"));
    }
  }

  private synchronized void setFailedIfNoParams(Throwable error) {
    if (params == null && !failed) {
      setFailed(error);
    }
  }

  void streamSessionOutput() {
    process
        .streamOutputLines()
        .forEach(
            line -> {
              if (line.isStdout() && line.line().contains("session_token")) {
                try (JsonReader reader = Json.createReader(new StringReader(line.line()))) {
                  JsonObject obj = reader.readObject();
                  int port = obj.getInt("port");
                  String sessionToken = obj.getString("session_token");
                  setParams(new ConnectParams(port, sessionToken));
                }
              } else {
                LOG.info(line.line());
              }
            });
  }

  public void shutdown() {
    if (executorService != null) {
      executorService.shutdown();
    }
    if (process != null) {
      process.close();
    }
  }

  private static IOException combineErrors(
      IOException originalError, String message, Throwable followupError) {
    IOException combined =
        new IOException(originalError.getMessage() + "\n" + message, originalError);
    combined.addSuppressed(followupError);
    return combined;
  }

  private IOException fallbackSessionError(String cliPath, Throwable error) {
    return combineErrors(
        downloadError,
        String.format("failed to use CLI from PATH \"%s\": %s", cliPath, error.getMessage()),
        error);
  }
}
