package io.dagger.client.engineconn;

import io.dagger.client.graphql.GraphQLClient;
import io.opentelemetry.api.trace.propagation.W3CTraceContextPropagator;
import io.opentelemetry.context.Context;
import java.io.IOException;
import java.nio.file.Path;
import java.util.HashMap;
import java.util.Map;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

public final class Connection {

  static final Logger LOG = LoggerFactory.getLogger(Connection.class);

  static volatile boolean module;

  private final GraphQLClient graphQLClient;
  private final CLISession session;

  Connection(GraphQLClient graphQLClient, CLISession session) {
    this.graphQLClient = graphQLClient;
    this.session = session;
  }

  public GraphQLClient getGraphQLClient() {
    return this.graphQLClient;
  }

  public void close() throws Exception {
    try {
      this.graphQLClient.close();
    } finally {
      if (this.session != null) {
        this.session.close();
      }
    }
  }

  /**
   * Marks this process as a module's. The engine that runs a module gives it its session, so from
   * then on a missing session is an error and no session is started.
   */
  public static void markModule() {
    module = true;
  }

  public static Connection get(String workingDir) throws IOException {
    return get(workingDir, false);
  }

  public static Connection get(String workingDir, boolean loadWorkspaceModules) throws IOException {
    return get(
        workingDir,
        loadWorkspaceModules,
        System.getenv("DAGGER_SESSION_PORT"),
        System.getenv("DAGGER_SESSION_TOKEN"),
        module);
  }

  static Connection get(
      String workingDir,
      boolean loadWorkspaceModules,
      String portStr,
      String sessionToken,
      boolean module)
      throws IOException {
    if (portStr == null || sessionToken == null) {
      if (module) {
        throw new IOException(
            "no Dagger session in the environment (DAGGER_SESSION_PORT and DAGGER_SESSION_TOKEN):"
                + " a module gets its session from the engine that runs it and never starts one");
      }
      CLISession session = CLISession.start(Path.of(workingDir), loadWorkspaceModules);
      return getConnection(session.port(), session.sessionToken(), session);
    }
    try {
      return getConnection(Integer.parseInt(portStr), sessionToken, null);
    } catch (NumberFormatException nfe) {
      throw new IOException("invalid port value in DAGGER_SESSION_PORT", nfe);
    }
  }

  /** The session this connection started, or null when it attached to one already running. */
  CLISession session() {
    return this.session;
  }

  static Connection getConnection(int port, String token, CLISession session) {
    // Not GlobalOpenTelemetry's propagators: reading the global locks it, and a later
    // GlobalOpenTelemetry.set, from Telemetry or from the host application, then fails.
    Map<String, String> headers = new HashMap<>();
    W3CTraceContextPropagator.getInstance()
        .inject(Context.current(), headers, (carrier, key, value) -> carrier.put(key, value));

    return new Connection(
        new GraphQLClient(String.format("http://127.0.0.1:%d/query", port), token, headers),
        session);
  }
}
