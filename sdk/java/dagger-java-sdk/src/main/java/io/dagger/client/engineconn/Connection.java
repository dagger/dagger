package io.dagger.client.engineconn;

import io.opentelemetry.api.GlobalOpenTelemetry;
import io.opentelemetry.context.Context;
import io.smallrye.graphql.client.dynamic.api.DynamicGraphQLClient;
import io.smallrye.graphql.client.vertx.dynamic.VertxDynamicGraphQLClientBuilder;
import io.vertx.core.Vertx;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.util.Base64;
import java.util.Optional;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

public final class Connection {

  static final Logger LOG = LoggerFactory.getLogger(Connection.class);

  private final DynamicGraphQLClient graphQLClient;
  private final Vertx vertx;
  private final Optional<CLIRunner> daggerRunner;

  Connection(DynamicGraphQLClient graphQLClient, Vertx vertx, Optional<CLIRunner> daggerRunner) {
    this.graphQLClient = graphQLClient;
    this.vertx = vertx;
    this.daggerRunner = daggerRunner;
  }

  public DynamicGraphQLClient getGraphQLClient() {
    return this.graphQLClient;
  }

  public void close() throws Exception {
    this.graphQLClient.close();
    this.vertx.close();
    this.daggerRunner.ifPresent(CLIRunner::shutdown);
  }

  static Optional<Connection> fromEnv() {
    LOG.info("Trying initializing connection with engine from environment variables...");
    String portStr = System.getenv("DAGGER_SESSION_PORT");
    String sessionToken = System.getenv("DAGGER_SESSION_TOKEN");
    if (portStr != null && sessionToken != null) {
      try {
        int port = Integer.parseInt(portStr);
        return Optional.of(getConnection(port, sessionToken, Optional.empty()));
      } catch (NumberFormatException nfe) {
        LOG.error("invalid port value in DAGGER_SESSION_PORT", nfe);
      }
    } else if (portStr == null && sessionToken != null) {
      LOG.error("DAGGER_SESSION_PORT is required when using DAGGER_SESSION_TOKEN");
    } else if (portStr != null && sessionToken == null) {
      LOG.error("DAGGER_SESSION_TOKEN is required when using DAGGER_SESSION_PORT");
    }
    return Optional.empty();
  }

  static Connection fromCLI(CLIRunner cliRunner) throws IOException {
    LOG.info("Trying initializing connection with engine from automatic provisioning...");
    try {
      cliRunner.start();
      ConnectParams connectParams = cliRunner.getConnectionParams();
      return getConnection(
          connectParams.getPort(), connectParams.getSessionToken(), Optional.of(cliRunner));
    } catch (IOException ioe) {
      LOG.error(ioe.getMessage(), ioe);
      cliRunner.shutdown();
      throw ioe;
    }
  }

  public static Connection get(String workingDir) throws IOException {
    Optional<Connection> connection = fromEnv();
    return connection.isPresent()
        ? connection.get()
        : fromCLI(new CLIRunner(workingDir, new CLIDownloader()));
  }

  private static Connection getConnection(int port, String token, Optional<CLIRunner> runner) {
    Vertx vertx = Vertx.vertx();
    // Inject Dagger Cloud token
    String encodedToken =
        Base64.getEncoder().encodeToString((token + ":").getBytes(StandardCharsets.UTF_8));

    VertxDynamicGraphQLClientBuilder clientBuilder =
        new VertxDynamicGraphQLClientBuilder()
            .vertx(vertx)
            .url(String.format("http://127.0.0.1:%d/query", port))
            .header("authorization", String.format("Basic %s", encodedToken));

    // Inject OpenTelemetry context into headers
    GlobalOpenTelemetry.getPropagators()
        .getTextMapPropagator()
        .inject(
            Context.current(), clientBuilder, (carrier, key, value) -> carrier.header(key, value));

    return new Connection(clientBuilder.build(), vertx, runner);
  }
}
