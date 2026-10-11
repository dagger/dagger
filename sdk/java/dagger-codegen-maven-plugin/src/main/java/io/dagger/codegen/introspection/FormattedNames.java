package io.dagger.codegen.introspection;

import jakarta.json.Json;
import jakarta.json.JsonArray;
import jakarta.json.JsonObject;
import jakarta.json.JsonReader;
import jakarta.json.JsonString;
import jakarta.json.JsonValue;
import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.TreeSet;
import java.util.regex.Pattern;

/**
 * Schema names formatted by the engine, for Java identifiers in Java's own conventions: members,
 * parameters, setters and accessors in camelCase with acronyms written like words ({@code asJson},
 * {@code withGpu}), classes in PascalCase with acronyms written like words ({@code JsonValue},
 * {@code Llm}), and enum constants in SCREAMING_SNAKE_CASE ({@code PER_SESSION}). Codegen doesn't
 * parse names itself; it asks the engine with {@code Query.formatIdentifiers}, or reads the names
 * file an online step wrote (see "Formatting names in codegen" in
 * hack/designs/identifier-casing.md).
 *
 * <p>The gate is the schema being generated: without {@code Query.formatIdentifiers} (schema views
 * before v1.0.0-0), nothing is formatted, and codegen keeps the schema names as before.
 */
public final class FormattedNames {

  /** The key of the member names (methods, parameters, accessors) in a names file. */
  public static final String MEMBERS = "CAMEL:CAPITALIZED";

  /** The key of the class names in a names file. */
  public static final String TYPES = "PASCAL:CAPITALIZED";

  /** The key of the enum constant names in a names file. */
  public static final String ENUM_VALUES = "SCREAMING_SNAKE:UPPERCASE";

  /** The formats Java codegen uses, keyed like a names file: {@code CASING:ACRONYMS}. */
  public static final List<String> FORMATS = List.of(MEMBERS, TYPES, ENUM_VALUES);

  /**
   * Bounds the size of the names sent per request. The names go to {@code dagger query} as a
   * command-line argument, which Linux caps at 128 KiB; the core schema's names fit in one.
   */
  static final int BATCH_BYTES = 64 << 10;

  /** Formats a batch of names in every format of {@link #FORMATS}, one alias per format. */
  static final String QUERY = query();

  private static final Pattern HAS_ALNUM = Pattern.compile("[A-Za-z0-9]");

  private FormattedNames() {}

  private static String query() {
    StringBuilder query = new StringBuilder("query FormatIdentifiers($names: [String!]!) {");
    for (int i = 0; i < FORMATS.size(); i++) {
      String[] format = FORMATS.get(i).split(":");
      query
          .append(" f")
          .append(i)
          .append(": formatIdentifiers(names: $names, casing: ")
          .append(format[0])
          .append(", acronyms: ")
          .append(format[1])
          .append(")");
    }
    return query.append(" }").toString();
  }

  /** Runs a GraphQL query with variables against the engine and returns its data as JSON. */
  @FunctionalInterface
  public interface QueryRunner {
    InputStream query(String query, String variables) throws IOException, InterruptedException;
  }

  /**
   * Formats the names of a schema through the engine, in batches, in every format Java codegen
   * uses. Returns the names keyed by format ({@link #MEMBERS}, {@link #TYPES}, {@link
   * #ENUM_VALUES}), like a names file, or null, formatting nothing, when the schema has no {@code
   * Query.formatIdentifiers}.
   */
  public static Map<String, Map<String, String>> fetch(byte[] schemaJson, QueryRunner runner)
      throws IOException, InterruptedException {
    JsonObject root = read(schemaJson);
    if (!hasFormatIdentifiers(root)) {
      return null;
    }
    Map<String, Map<String, String>> formatted = new HashMap<>();
    for (String format : FORMATS) {
      formatted.put(format, new HashMap<>());
    }
    for (List<String> batch : batches(names(root), BATCH_BYTES)) {
      String variables =
          Json.createObjectBuilder()
              .add("names", Json.createArrayBuilder(batch))
              .build()
              .toString();
      JsonObject data;
      try (InputStream in = runner.query(QUERY, variables)) {
        data = read(in.readAllBytes());
      }
      for (int i = 0; i < FORMATS.size(); i++) {
        JsonValue result = data.get("f" + i);
        if (!(result instanceof JsonArray)) {
          throw new IOException("format names: no formatIdentifiers in the response");
        }
        JsonArray out = (JsonArray) result;
        if (out.size() != batch.size()) {
          throw new IOException(
              "format names: sent " + batch.size() + " names, got " + out.size() + " back");
        }
        Map<String, String> names = formatted.get(FORMATS.get(i));
        for (int j = 0; j < batch.size(); j++) {
          names.put(batch.get(j), out.getString(j));
        }
      }
    }
    return formatted;
  }

  /**
   * Reads the names Java codegen uses from a names file, as {@code codegen introspect --names-out}
   * writes it: {@code {"CAMEL:CAPITALIZED": {"withGPU": "withGpu", ...}, "PASCAL:CAPITALIZED":
   * {...}, ...}}, keyed by format. Formats Java codegen doesn't use are left out, and a format the
   * file doesn't have keeps the schema's names. Returns null when the file has none of them (the
   * schema has no {@code Query.formatIdentifiers}).
   */
  public static Map<String, Map<String, String>> read(InputStream namesJson) throws IOException {
    JsonObject root = read(namesJson.readAllBytes());
    Map<String, Map<String, String>> formatted = new HashMap<>();
    for (String format : FORMATS) {
      JsonValue names = root.get(format);
      if (!(names instanceof JsonObject)) {
        continue;
      }
      Map<String, String> byName = new HashMap<>();
      for (Map.Entry<String, JsonValue> e : ((JsonObject) names).entrySet()) {
        if (e.getValue() instanceof JsonString) {
          byName.put(e.getKey(), ((JsonString) e.getValue()).getString());
        }
      }
      formatted.put(format, byName);
    }
    return formatted.isEmpty() ? null : formatted;
  }

  static boolean hasFormatIdentifiers(JsonObject root) {
    JsonObject schema = object(root, "__schema");
    if (schema == null) {
      return false;
    }
    JsonObject queryType = object(schema, "queryType");
    String queryName = queryType == null ? "Query" : queryType.getString("name", "Query");
    for (JsonObject type : objects(schema, "types")) {
      if (queryName.equals(type.getString("name", null))) {
        return objects(type, "fields").stream()
            .anyMatch(field -> "formatIdentifiers".equals(field.getString("name", null)));
      }
    }
    return false;
  }

  /**
   * The distinct type, field, argument, input field and enum value names in the schema, sorted,
   * leaving out introspection names ("__" prefix) and names the engine can't format (non-ASCII, or
   * with no letters or digits).
   */
  static List<String> names(JsonObject root) {
    TreeSet<String> names = new TreeSet<>();
    JsonObject schema = object(root, "__schema");
    if (schema == null) {
      return List.of();
    }
    for (JsonObject type : objects(schema, "types")) {
      String typeName = type.getString("name", "");
      if (typeName.startsWith("__")) {
        continue;
      }
      addName(names, typeName);
      for (JsonObject field : objects(type, "fields")) {
        addName(names, field.getString("name", ""));
        for (JsonObject arg : objects(field, "args")) {
          addName(names, arg.getString("name", ""));
        }
      }
      for (JsonObject inputField : objects(type, "inputFields")) {
        addName(names, inputField.getString("name", ""));
      }
      for (JsonObject enumValue : objects(type, "enumValues")) {
        addName(names, enumValue.getString("name", ""));
      }
    }
    return new ArrayList<>(names);
  }

  /**
   * Splits names into batches of at most maxBytes as a JSON array (but at least one name each).
   * Schema names need no escaping, so each takes its length plus quotes and a comma.
   */
  static List<List<String>> batches(List<String> names, int maxBytes) {
    List<List<String>> batches = new ArrayList<>();
    int start = 0;
    while (start < names.size()) {
      int end = start;
      int size = 2;
      while (end < names.size()
          && (end == start || size + names.get(end).length() + 3 <= maxBytes)) {
        size += names.get(end).length() + 3;
        end++;
      }
      batches.add(names.subList(start, end));
      start = end;
    }
    return batches;
  }

  private static void addName(TreeSet<String> names, String name) {
    if (name.startsWith("__")
        || !name.chars().allMatch(c -> c < 0x80)
        || !HAS_ALNUM.matcher(name).find()) {
      return;
    }
    names.add(name);
  }

  private static JsonObject object(JsonObject parent, String key) {
    JsonValue value = parent.get(key);
    return value instanceof JsonObject ? (JsonObject) value : null;
  }

  private static List<JsonObject> objects(JsonObject parent, String key) {
    JsonValue value = parent.get(key);
    if (!(value instanceof JsonArray)) {
      return List.of();
    }
    return ((JsonArray) value).stream().map(JsonValue::asJsonObject).toList();
  }

  private static JsonObject read(byte[] json) {
    try (JsonReader reader = Json.createReader(new ByteArrayInputStream(json))) {
      return reader.readObject();
    }
  }
}
