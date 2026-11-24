package io.dagger.client.graphql;

import jakarta.json.Json;
import jakarta.json.JsonArray;
import jakarta.json.JsonObject;
import jakarta.json.JsonReader;
import java.io.StringReader;
import java.util.List;

/**
 * A parsed GraphQL response payload ({@code {"data": ..., "errors": [...]}}).
 */
public final class GraphQLResponse {

  private final JsonObject data;
  private final List<GraphQLError> errors;

  private GraphQLResponse(JsonObject data, List<GraphQLError> errors) {
    this.data = data;
    this.errors = errors;
  }

  static GraphQLResponse fromBody(String body) {
    try (JsonReader reader = Json.createReader(new StringReader(body))) {
      JsonObject payload = reader.readObject();
      JsonObject data = (payload.get("data") instanceof JsonObject obj) ? obj : null;
      List<GraphQLError> errors =
          (payload.get("errors") instanceof JsonArray array)
              ? array.stream()
                  .filter(v -> v instanceof JsonObject)
                  .map(v -> GraphQLError.fromJson((JsonObject) v))
                  .toList()
              : List.of();
      return new GraphQLResponse(data, errors);
    }
  }

  public JsonObject getData() {
    return data;
  }

  public List<GraphQLError> getErrors() {
    return errors;
  }

  public boolean hasError() {
    return !errors.isEmpty();
  }
}
