package io.dagger.client.graphql;

import java.util.List;
import java.util.Map;
import java.util.stream.Collectors;

/**
 * Renders Java values as GraphQL literals. Supported inputs are the normalized argument values
 * produced by io.dagger.client.Arguments: null, String, Integer, Long, Boolean, List and Map
 * (input objects).
 */
public final class GraphQLValues {

  private GraphQLValues() {}

  /** GraphQL string literals use the same escaping rules as JSON strings. */
  public static String quote(String value) {
    StringBuilder sb = new StringBuilder(value.length() + 2);
    sb.append('"');
    for (int i = 0; i < value.length(); i++) {
      char c = value.charAt(i);
      switch (c) {
        case '"' -> sb.append("\\\"");
        case '\\' -> sb.append("\\\\");
        case '\b' -> sb.append("\\b");
        case '\f' -> sb.append("\\f");
        case '\n' -> sb.append("\\n");
        case '\r' -> sb.append("\\r");
        case '\t' -> sb.append("\\t");
        default -> {
          if (c < 0x20) {
            sb.append(String.format("\\u%04x", (int) c));
          } else {
            sb.append(c);
          }
        }
      }
    }
    sb.append('"');
    return sb.toString();
  }

  public static String format(Object value) {
    if (value == null) {
      return "null";
    }
    if (value instanceof String s) {
      return quote(s);
    }
    if (value instanceof List<?> list) {
      return list.stream().map(GraphQLValues::format).collect(Collectors.joining(",", "[", "]"));
    }
    if (value instanceof Map<?, ?> map) {
      return map.entrySet().stream()
          .map(e -> e.getKey() + ":" + format(e.getValue()))
          .collect(Collectors.joining(",", "{", "}"));
    }
    return String.valueOf(value);
  }
}
