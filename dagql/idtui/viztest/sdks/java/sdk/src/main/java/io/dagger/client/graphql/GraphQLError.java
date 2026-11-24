package io.dagger.client.graphql;

import jakarta.json.JsonArray;
import jakarta.json.JsonObject;
import jakarta.json.JsonString;
import jakarta.json.JsonValue;
import java.util.Arrays;
import java.util.LinkedHashMap;
import java.util.Map;

/**
 * A single GraphQL error. String values in path and extensions are unwrapped to plain Strings;
 * structured extension values (arrays, objects) stay jakarta.json values.
 */
public final class GraphQLError {

  private final String message;
  private final Object[] path;
  private final Map<String, Object> extensions;

  private GraphQLError(String message, Object[] path, Map<String, Object> extensions) {
    this.message = message;
    this.path = path;
    this.extensions = extensions;
  }

  static GraphQLError fromJson(JsonObject error) {
    String message = error.getString("message", "");
    Object[] path =
        (error.get("path") instanceof JsonArray array)
            ? array.stream().map(GraphQLError::unwrap).toArray()
            : new Object[0];
    Map<String, Object> extensions = new LinkedHashMap<>();
    if (error.get("extensions") instanceof JsonObject ext) {
      ext.forEach((key, value) -> extensions.put(key, unwrap(value)));
    }
    return new GraphQLError(message, path, extensions);
  }

  private static Object unwrap(JsonValue value) {
    return switch (value.getValueType()) {
      case STRING -> ((JsonString) value).getString();
      case TRUE -> Boolean.TRUE;
      case FALSE -> Boolean.FALSE;
      case NULL -> null;
      default -> value;
    };
  }

  public String getMessage() {
    return message;
  }

  public Object[] getPath() {
    return path;
  }

  public Map<String, Object> getExtensions() {
    return extensions;
  }

  @Override
  public String toString() {
    return "GraphQLError{message="
        + message
        + ", path="
        + Arrays.toString(path)
        + ", extensions="
        + extensions
        + "}";
  }
}
