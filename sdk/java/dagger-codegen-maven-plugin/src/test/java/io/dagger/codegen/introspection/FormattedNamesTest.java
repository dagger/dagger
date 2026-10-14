package io.dagger.codegen.introspection;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import jakarta.json.Json;
import jakarta.json.JsonArrayBuilder;
import jakarta.json.JsonObject;
import jakarta.json.JsonObjectBuilder;
import jakarta.json.JsonReader;
import jakarta.json.JsonString;
import jakarta.json.JsonValue;
import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.StringReader;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

class FormattedNamesTest {

  /** A request FormattedNames sent: the GraphQL document and its variables. */
  private record Request(String query, JsonObject variables) {}

  /**
   * Fakes the engine: answers each formatIdentifiers alias (f0, f1, ...) with each name prefixed by
   * the alias, and records the requests.
   */
  private static FormattedNames.QueryRunner engine(List<Request> requests) {
    return (query, variables) -> {
      JsonObject vars = read(variables);
      requests.add(new Request(query, vars));
      JsonObjectBuilder data = Json.createObjectBuilder();
      for (int i = 0; i < FormattedNames.FORMATS.size(); i++) {
        JsonArrayBuilder out = Json.createArrayBuilder();
        for (JsonValue name : vars.getJsonArray("names")) {
          out.add("f" + i + "_" + ((JsonString) name).getString());
        }
        data.add("f" + i, out);
      }
      return stream(data.build().toString());
    };
  }

  @Test
  void formatsNamesWhenTheSchemaHasFormatIdentifiers() throws Exception {
    List<Request> requests = new ArrayList<>();
    Map<String, Map<String, String>> names = FormattedNames.fetch(schema(true), engine(requests));

    assertThat(requests).hasSize(1);
    Request request = requests.get(0);
    // The names go as a variable, in one request formatting them in every format Java uses:
    // camelCase members and PascalCase classes with acronyms written like words, and
    // SCREAMING_SNAKE_CASE enum constants.
    assertThat(request.query())
        .contains("$names: [String!]!")
        .contains("f0: formatIdentifiers(names: $names, casing: CAMEL, acronyms: CAPITALIZED)")
        .contains("f1: formatIdentifiers(names: $names, casing: PASCAL, acronyms: CAPITALIZED)")
        .contains(
            "f2: formatIdentifiers(names: $names, casing: SCREAMING_SNAKE, acronyms: UPPERCASE)");
    assertThat(FormattedNames.FORMATS)
        .containsExactly(FormattedNames.MEMBERS, FormattedNames.TYPES, FormattedNames.ENUM_VALUES);
    // Type, field, argument and enum value names, sorted, leaving out introspection names and
    // names with no letters or digits.
    assertThat(request.variables().getJsonArray("names").getValuesAs(JsonString::getString))
        .containsExactly(
            "Kind",
            "Query",
            "SHARED",
            "acronyms",
            "casing",
            "formatIdentifiers",
            "htmlURL",
            "names");

    // Results map back to the names in input order, keyed by format like a names file.
    assertThat(names).containsOnlyKeys(FormattedNames.FORMATS);
    assertThat(names.get("CAMEL:CAPITALIZED"))
        .containsEntry("htmlURL", "f0_htmlURL")
        .containsEntry("SHARED", "f0_SHARED")
        .hasSize(8);
    assertThat(names.get("PASCAL:CAPITALIZED")).containsEntry("Kind", "f1_Kind").hasSize(8);
    assertThat(names.get("SCREAMING_SNAKE:UPPERCASE"))
        .containsEntry("SHARED", "f2_SHARED")
        .hasSize(8);
  }

  @Test
  void formatsNothingWithoutFormatIdentifiers() throws Exception {
    Map<String, Map<String, String>> names =
        FormattedNames.fetch(
            schema(false),
            (query, variables) -> {
              throw new AssertionError("unexpected query: " + query);
            });
    assertThat(names).isNull();
  }

  @Test
  void batchesNames() throws Exception {
    StringBuilder fields = new StringBuilder();
    // 4000 names of 20 bytes: more than BATCH_BYTES encoded.
    for (int i = 0; i < 4000; i++) {
      fields.append(String.format(",{\"name\":\"field%015d\",\"args\":[]}", i));
    }
    String json =
        "{\"__schema\":{\"queryType\":{\"name\":\"Query\"},\"types\":[{\"kind\":\"OBJECT\","
            + "\"name\":\"Query\",\"fields\":[{\"name\":\"formatIdentifiers\",\"args\":[]}"
            + fields
            + "]}]}}";
    List<Request> requests = new ArrayList<>();
    Map<String, Map<String, String>> names =
        FormattedNames.fetch(json.getBytes(StandardCharsets.UTF_8), engine(requests));

    assertThat(requests).hasSize(2);
    for (Request request : requests) {
      assertThat(request.variables().getJsonArray("names").toString().length())
          .isLessThanOrEqualTo(FormattedNames.BATCH_BYTES);
    }
    // Query, formatIdentifiers and field0..field3999.
    assertThat(names.get(FormattedNames.MEMBERS)).hasSize(4002).containsEntry("Query", "f0_Query");
  }

  @Test
  void batchesAtLeastOneName() {
    assertThat(FormattedNames.batches(List.of(), 10)).isEmpty();
    assertThat(FormattedNames.batches(List.of("ab", "cd", "efghijklmn", "o"), 12))
        .containsExactly(List.of("ab", "cd"), List.of("efghijklmn"), List.of("o"));
  }

  @Test
  void rejectsAShortAnswer() {
    assertThatThrownBy(
            () ->
                FormattedNames.fetch(
                    schema(true),
                    (query, variables) -> stream("{\"f0\":[\"x\"],\"f1\":[\"x\"],\"f2\":[\"x\"]}")))
        .isInstanceOf(IOException.class)
        .hasMessageContaining("sent 8 names, got 1 back");
  }

  @Test
  void readsTheNamesFile() throws Exception {
    Map<String, Map<String, String>> names =
        FormattedNames.read(
            stream(
                "{\"CAMEL:CAPITALIZED\":{\"withGPU\":\"withGpu\"},"
                    + "\"PASCAL:CAPITALIZED\":{\"JSONValue\":\"JsonValue\"},"
                    + "\"SCREAMING_SNAKE:UPPERCASE\":{\"PerSession\":\"PER_SESSION\"},"
                    + "\"SNAKE:UPPERCASE\":{\"withGPU\":\"with_gpu\"}}"));
    // The formats Java codegen uses, leaving the others out.
    assertThat(names).containsOnlyKeys(FormattedNames.FORMATS);
    assertThat(names.get(FormattedNames.MEMBERS)).containsExactly(Map.entry("withGPU", "withGpu"));
    assertThat(names.get(FormattedNames.TYPES))
        .containsExactly(Map.entry("JSONValue", "JsonValue"));
    assertThat(names.get(FormattedNames.ENUM_VALUES))
        .containsExactly(Map.entry("PerSession", "PER_SESSION"));

    // A file with only some of the formats has only those.
    assertThat(FormattedNames.read(stream("{\"CAMEL:CAPITALIZED\":{\"a\":\"a\"}}")))
        .containsOnlyKeys(FormattedNames.MEMBERS);

    // The names file of a schema without Query.formatIdentifiers is empty.
    assertThat(FormattedNames.read(stream("{}"))).isNull();
  }

  private static byte[] schema(boolean withFormatIdentifiers) {
    String formatIdentifiers =
        withFormatIdentifiers
            ? "{\"name\":\"formatIdentifiers\",\"args\":[{\"name\":\"names\"},"
                + "{\"name\":\"casing\"},{\"name\":\"acronyms\"}]},"
            : "";
    return ("{\"__schema\":{\"queryType\":{\"name\":\"Query\"},\"types\":["
            + "{\"kind\":\"OBJECT\",\"name\":\"Query\",\"fields\":["
            + formatIdentifiers
            + "{\"name\":\"htmlURL\",\"args\":[]}]},"
            + "{\"kind\":\"ENUM\",\"name\":\"Kind\",\"enumValues\":[{\"name\":\"SHARED\"}]},"
            + "{\"kind\":\"OBJECT\",\"name\":\"__Type\",\"fields\":[{\"name\":\"possibleTypes\"}]},"
            + "{\"kind\":\"SCALAR\",\"name\":\"_\"}"
            + "]}}")
        .getBytes(StandardCharsets.UTF_8);
  }

  private static InputStream stream(String json) {
    return new ByteArrayInputStream(json.getBytes(StandardCharsets.UTF_8));
  }

  private static JsonObject read(String json) {
    try (JsonReader reader = Json.createReader(new StringReader(json))) {
      return reader.readObject();
    }
  }
}
