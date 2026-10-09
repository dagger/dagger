package io.dagger.codegen.introspection;

import jakarta.json.Json;
import jakarta.json.JsonArray;
import jakarta.json.JsonArrayBuilder;
import jakarta.json.JsonObject;
import jakarta.json.JsonObjectBuilder;
import jakarta.json.JsonReader;
import jakarta.json.JsonValue;
import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.TreeSet;
import java.util.regex.Pattern;

/**
 * Fetches the words of a schema's names from the engine's {@code Query.identifier} API, for schema
 * JSON from an introspection query, which can't carry them. The engine writes the same words to the
 * {@code __identifiers} key of the schema JSON it hands to modules (see "Schema JSON words" in
 * hack/designs/identifier-casing.md).
 */
public final class IdentifierFetcher {

  /** Bounds the number of names parsed per identifier request. */
  static final int BATCH_SIZE = 500;

  private static final Pattern NAME = Pattern.compile("[_A-Za-z][_0-9A-Za-z]*");
  private static final Pattern HAS_ALNUM = Pattern.compile("[A-Za-z0-9]");

  private IdentifierFetcher() {}

  /** Runs a GraphQL query against the engine and returns its data as JSON. */
  @FunctionalInterface
  public interface QueryRunner {
    InputStream query(String query) throws IOException, InterruptedException;
  }

  /**
   * Adds {@code __identifiers} to schema JSON that lacks it. Returns the JSON unchanged when it
   * already has words, or when the engine has no {@code Query.identifier} (engine views before
   * v1.0.0-0), so codegen keeps its own conversion.
   */
  public static byte[] addIdentifiers(byte[] schemaJson, QueryRunner runner)
      throws IOException, InterruptedException {
    JsonObject root = read(schemaJson);
    if (root.containsKey("__identifiers") || !hasIdentifierField(root)) {
      return schemaJson;
    }

    List<String> names = names(root);
    JsonObjectBuilder identifiers = Json.createObjectBuilder();
    for (int start = 0; start < names.size(); start += BATCH_SIZE) {
      List<String> batch = names.subList(start, Math.min(names.size(), start + BATCH_SIZE));
      StringBuilder query = new StringBuilder("query Identifiers {");
      for (int i = 0; i < batch.size(); i++) {
        query
            .append(" i")
            .append(i)
            .append(": identifier(name: \"")
            .append(batch.get(i))
            .append("\") { words { kind text suffix term { capitalized } } }");
      }
      query.append(" }");

      JsonObject data;
      try (InputStream in = runner.query(query.toString())) {
        data = read(in.readAllBytes());
      }
      for (int i = 0; i < batch.size(); i++) {
        String name = batch.get(i);
        JsonValue result = data.get("i" + i);
        if (!(result instanceof JsonObject)) {
          throw new IOException("identifier query: no result for \"" + name + "\"");
        }
        identifiers.add(name, words(((JsonObject) result).getJsonArray("words")));
      }
    }

    return Json.createObjectBuilder(root)
        .add("__identifiers", identifiers)
        .build()
        .toString()
        .getBytes(StandardCharsets.UTF_8);
  }

  /** Converts Identifier.words to the shape of the schema JSON's words. */
  private static JsonArrayBuilder words(JsonArray words) {
    JsonArrayBuilder out = Json.createArrayBuilder();
    if (words == null) {
      return out;
    }
    for (JsonValue value : words) {
      JsonObject word = value.asJsonObject();
      String text = word.getString("text");
      // The word's CAPITALIZED form: the dictionary entry's, else the first letter capitalized
      // and the rest lowercase.
      String capitalized;
      JsonValue term = word.get("term");
      if (term instanceof JsonObject) {
        capitalized = ((JsonObject) term).getString("capitalized");
      } else {
        String lower = text.toLowerCase(Locale.ROOT);
        capitalized =
            lower.isEmpty()
                ? lower
                : lower.substring(0, 1).toUpperCase(Locale.ROOT) + lower.substring(1);
      }
      out.add(
          Json.createObjectBuilder()
              .add("kind", word.getString("kind"))
              .add("text", text)
              .add("suffix", word.getString("suffix", ""))
              .add("capitalized", capitalized));
    }
    return out;
  }

  private static boolean hasIdentifierField(JsonObject root) {
    JsonObject schema = object(root, "__schema");
    if (schema == null) {
      return false;
    }
    JsonObject queryType = object(schema, "queryType");
    String queryName = queryType == null ? "Query" : queryType.getString("name", "Query");
    for (JsonObject type : objects(schema, "types")) {
      if (queryName.equals(type.getString("name", null))) {
        return objects(type, "fields").stream()
            .anyMatch(field -> "identifier".equals(field.getString("name", null)));
      }
    }
    return false;
  }

  /**
   * The distinct type, field, argument, input field and enum value names in the schema, sorted,
   * leaving out introspection names ("__" prefix) and names with no letters or digits.
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

  private static void addName(TreeSet<String> names, String name) {
    if (name.startsWith("__") || !NAME.matcher(name).matches() || !HAS_ALNUM.matcher(name).find()) {
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
