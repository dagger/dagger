package io.dagger.client.graphql;

import java.io.IOException;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.Base64;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/**
 * Minimal synchronous GraphQL-over-HTTP client for the Dagger session endpoint.
 */
public final class GraphQLClient implements AutoCloseable {

  private final HttpClient http;
  private final URI endpoint;
  private final Map<String, String> headers;
  private final ExecutorService executor;

  public GraphQLClient(String url, String sessionToken, Map<String, String> extraHeaders) {
    this.endpoint = URI.create(url);
    this.headers = new LinkedHashMap<>(extraHeaders);
    String encodedToken =
        Base64.getEncoder()
            .encodeToString((sessionToken + ":").getBytes(StandardCharsets.UTF_8));
    this.headers.put("authorization", "Basic " + encodedToken);
    // Daemon threads so a module entrypoint exits even if close() is skipped
    this.executor =
        Executors.newCachedThreadPool(
            runnable -> {
              Thread t = new Thread(runnable, "dagger-graphql-client");
              t.setDaemon(true);
              return t;
            });
    this.http =
        HttpClient.newBuilder()
            .version(HttpClient.Version.HTTP_1_1)
            .connectTimeout(Duration.ofSeconds(10))
            .executor(executor)
            .build();
  }

  /**
   * Execute a GraphQL query document and return the parsed response. No request timeout is set:
   * queries routinely block for as long as the underlying pipeline runs.
   */
  public GraphQLResponse executeQuery(String query)
      throws ExecutionException, InterruptedException {
    HttpRequest.Builder builder =
        HttpRequest.newBuilder(endpoint)
            .header("content-type", "application/json")
            .POST(
                HttpRequest.BodyPublishers.ofString(
                    "{\"query\":" + GraphQLValues.quote(query) + "}", StandardCharsets.UTF_8));
    headers.forEach(builder::header);
    try {
      HttpResponse<String> response =
          http.send(builder.build(), HttpResponse.BodyHandlers.ofString());
      if (response.statusCode() != 200) {
        throw new ExecutionException(
            new IOException(
                "GraphQL request failed with HTTP "
                    + response.statusCode()
                    + ": "
                    + response.body()));
      }
      return GraphQLResponse.fromBody(response.body());
    } catch (IOException ioe) {
      throw new ExecutionException(ioe);
    }
  }

  @Override
  public void close() {
    executor.shutdown();
  }
}
