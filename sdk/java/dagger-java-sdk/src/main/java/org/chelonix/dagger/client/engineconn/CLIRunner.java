package org.chelonix.dagger.client.engineconn;

import com.ongres.process.FluentProcess;
import jakarta.json.Json;
import jakarta.json.JsonObject;
import jakarta.json.JsonReader;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.io.StringReader;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

class CLIRunner implements Runnable {

    static final Logger LOG = LoggerFactory.getLogger(CLIRunner.class);

    private final FluentProcess process;
    private ConnectParams params;
    private boolean failed = false;
    private final ExecutorService executorService;

    private static String getCLIPath() throws IOException {
        String cliBinPath = System.getenv("_EXPERIMENTAL_DAGGER_CLI_BIN");
        if (cliBinPath == null) {
            cliBinPath = new CLIDownloader().downloadCLI();
        }
        return cliBinPath;
    }

    public CLIRunner(String workingDir) throws IOException {
        String bin = getCLIPath();
        this.process = FluentProcess.start(bin, "session",
                        "--workdir", workingDir,
                        "--label", "dagger.io/sdk.name:java",
                        "--label", "dagger.io/sdk.version:" + CLIDownloader.CLI_VERSION)
                .withAllowedExitCodes(137);
        this.executorService = Executors.newSingleThreadExecutor(r -> new Thread(r, "dagger-runner"));

    }

    synchronized ConnectParams getConnectionParams() throws IOException {
        while (params == null) {
            try {
                if (failed) {
                    throw new IOException("Could not connect to Dagger engine");
                }
                wait();
            } catch (InterruptedException e) {
            }
        }
        return params;
    }

    private synchronized void setFailed() {
        this.failed = true;
        notifyAll();
    }


    synchronized void setParams(ConnectParams params) {
        this.params = params;
        notifyAll();
    }

    public void start() {
        executorService.execute(this);
    }

    @Override
    public void run() {
        try {
            process.streamOutputLines().forEach(line -> {
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
        } catch (RuntimeException e) {
            if (!(e.getCause() instanceof IOException
                    && "Stream closed".equals(e.getCause().getMessage()))) {
                LOG.error(e.getMessage(), e);
                setFailed();
                throw e;
            }
        }
    }

    public void shutdown() {
        executorService.shutdown();
        process.close();
    }
}
