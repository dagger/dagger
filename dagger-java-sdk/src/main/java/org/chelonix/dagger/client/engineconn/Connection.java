package org.chelonix.dagger.client.engineconn;

import com.ongres.process.FluentProcess;
import io.smallrye.graphql.client.dynamic.api.DynamicGraphQLClient;
import io.smallrye.graphql.client.vertx.dynamic.VertxDynamicGraphQLClientBuilder;
import io.vertx.core.Vertx;
import jakarta.json.bind.Jsonb;
import jakarta.json.bind.JsonbBuilder;
import jakarta.json.bind.annotation.JsonbProperty;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.*;
import java.nio.charset.StandardCharsets;
import java.util.Base64;
import java.util.Optional;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

public final class Connection {

    static final Logger LOG = LoggerFactory.getLogger(Connection.class);

    private final DynamicGraphQLClient graphQLClient;
    private final Vertx vertx;
    private final Optional<FluentProcess> daggerProc;

    Connection(DynamicGraphQLClient graphQLClient, Vertx vertx, Optional<FluentProcess> daggerProc) {
        this.graphQLClient = graphQLClient;
        this.vertx = vertx;
        this.daggerProc = daggerProc;
    }

    public DynamicGraphQLClient getGraphQLClient() {
        return this.graphQLClient;
    }

    public void close() throws Exception {
        this.graphQLClient.close();
        this.vertx.close();
        this.daggerProc.ifPresent(FluentProcess::close);
    }

    private static class CLIRunner implements Runnable {
                ;
        private FluentProcess process;
        private ConnectParams params;

        public CLIRunner(FluentProcess process) {
            this.process = process;
        }

        synchronized ConnectParams getConnectionParams() {
            while (params == null) {
                try {
                    wait();
                } catch (InterruptedException e) {
                }
            }
            return params;
        }

        synchronized void setParams(ConnectParams params) {
            this.params = params;
            notifyAll();
        }

        @Override
        public void run() {
            process = process.withoutCloseAfterLast();
            try {
                process.streamOutputLines().forEach(line -> {
                    if (line.isStdout() && line.line().contains("session_token")) {
                        Jsonb jsonb = JsonbBuilder.create();
                        ConnectParams connectParams = jsonb.fromJson(line.line(), ConnectParams.class);
                        setParams(connectParams);
                    } else {
                        LOG.info(line.line());
                    }
                });
            } catch (RuntimeException e) {
                if (! (e.getCause() instanceof IOException
                        && "Stream closed".equals(e.getCause().getMessage()))) {
                    throw e;
                }
            }
        }
    }

    public static class ConnectParams {
        private int port;

        @JsonbProperty("session_token")
        private String sessionToken;

        public int getPort() {
            return port;
        }

        @Override
        public String toString() {
            return "ConnectParams{" +
                    "port=" + port +
                    ", sessionToken='" + sessionToken + '\'' +
                    '}';
        }

        public void setPort(int port) {
            this.port = port;
        }

        public String getSessionToken() {
            return sessionToken;
        }

        public void setSessionToken(String sessionToken) {
            this.sessionToken = sessionToken;
        }
    }

    private static String getCLIPath() throws IOException {
        String cliBinPath = System.getenv("_EXPERIMENTAL_DAGGER_CLI_BIN");
        if (cliBinPath == null) {
            cliBinPath = new CLIDownloader().downloadCLI();
        }
        return cliBinPath;
    }

    private static Optional<Connection> fromEnv() {
        String portStr = System.getenv("DAGGER_SESSION_PORT");
        if (portStr == null) {
            return Optional.empty();
        }
        try {
            int port = Integer.parseInt(portStr);
            String token = System.getenv("DAGGER_SESSION_TOKEN");
            if (token == null) {
                throw new IllegalArgumentException("DAGGER_SESSION_TOKEN is required when using DAGGER_SESSION_PORT");
            }
            return Optional.of(getConnection(port, token, Optional.empty()));
        } catch (NumberFormatException nfe) {
            throw new IllegalArgumentException("invalid port in DAGGER_SESSION_PORT", nfe);
        }
    }

    private static Connection fromCLI(String workingDir) throws IOException {
        String bin = getCLIPath();
        FluentProcess process = FluentProcess.start(bin, "session",
                "--workdir", workingDir,
                "--label", "dagger.io/sdk.name:java",
                "--label", "dagger.io/sdk.version:" + CLIDownloader.CLI_VERSION)
                .withAllowedExitCodes(137);
        CLIRunner cliRunner = new CLIRunner(process);
        ExecutorService executorService = Executors.newSingleThreadExecutor(r -> new Thread(r, "dagger-runner"));
        executorService.execute(cliRunner);
        ConnectParams connectParams = cliRunner.getConnectionParams();
        return getConnection(connectParams.getPort(), connectParams.getSessionToken(), Optional.of(process));
    }

    public static Connection get(String workingDir) throws IOException {
        return fromEnv().orElse(fromCLI(workingDir));
    }

    private static Connection getConnection(int port, String token, Optional<FluentProcess> process) {
        Vertx vertx = Vertx.vertx();
        String encodedToken = Base64.getEncoder().encodeToString((token + ":").getBytes(StandardCharsets.UTF_8));
        DynamicGraphQLClient dynamicGraphQLClient = new VertxDynamicGraphQLClientBuilder()
                .vertx(vertx)
                .url(String.format("http://127.0.0.1:%d/query", port))
                .header("authorization", "Basic " + encodedToken)
                .build();

        return new Connection(dynamicGraphQLClient, vertx, process);
    }
}
