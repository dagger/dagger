package io.dagger.codegen.introspection;

import static org.assertj.core.api.Assertions.assertThat;

import jakarta.json.Json;
import jakarta.json.JsonObject;
import jakarta.json.JsonReader;
import java.io.ByteArrayInputStream;
import java.io.StringReader;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.junit.jupiter.api.Test;

class IdentifierFetcherTest {

  private static final Pattern FIELD =
      Pattern.compile("(i\\d+): identifier\\(name: \"([^\"]+)\"\\)");

  @Test
  void fetchesWordsWhenTheEngineHasIdentifier() throws Exception {
    List<String> queries = new ArrayList<>();
    byte[] out =
        IdentifierFetcher.addIdentifiers(
            schema(true),
            query -> {
              queries.add(query);
              StringBuilder data = new StringBuilder("{");
              Matcher m = FIELD.matcher(query);
              while (m.find()) {
                if (data.length() > 1) {
                  data.append(',');
                }
                data.append('"').append(m.group(1)).append("\":").append(words(m.group(2)));
              }
              return new ByteArrayInputStream(
                  data.append('}').toString().getBytes(StandardCharsets.UTF_8));
            });

    assertThat(queries).hasSize(1);
    JsonObject identifiers = read(out).getJsonObject("__identifiers");
    assertThat(identifiers.keySet())
        .containsExactlyInAnyOrder("Query", "identifier", "name", "htmlURL", "Kind", "SHARED");
    // A dictionary term's capitalized form comes from the term; others are derived.
    assertThat(identifiers.getJsonArray("htmlURL").toString())
        .isEqualTo(
            "[{\"kind\":\"ACRONYM\",\"text\":\"HTML\",\"suffix\":\"\",\"capitalized\":\"Html\"},"
                + "{\"kind\":\"ACRONYM\",\"text\":\"URL\",\"suffix\":\"\",\"capitalized\":\"Url\"}]");
    assertThat(identifiers.getJsonArray("name").toString())
        .isEqualTo(
            "[{\"kind\":\"WORD\",\"text\":\"name\",\"suffix\":\"\",\"capitalized\":\"Name\"}]");
    // The schema itself is untouched.
    assertThat(read(out).getJsonObject("__schema"))
        .isEqualTo(read(schema(true)).getJsonObject("__schema"));
  }

  @Test
  void leavesOlderSchemasAlone() throws Exception {
    byte[] in = schema(false);
    byte[] out =
        IdentifierFetcher.addIdentifiers(
            in,
            query -> {
              throw new AssertionError("unexpected query: " + query);
            });
    assertThat(out).isSameAs(in);
  }

  @Test
  void batchesNames() throws Exception {
    StringBuilder fields = new StringBuilder();
    for (int i = 0; i < IdentifierFetcher.BATCH_SIZE + 1; i++) {
      fields.append(",{\"name\":\"field").append(i).append("\",\"args\":[]}");
    }
    String json =
        "{\"__schema\":{\"queryType\":{\"name\":\"Query\"},\"types\":[{\"kind\":\"OBJECT\","
            + "\"name\":\"Query\",\"fields\":[{\"name\":\"identifier\",\"args\":[]}"
            + fields
            + "]}]}}";
    List<String> queries = new ArrayList<>();
    IdentifierFetcher.addIdentifiers(
        json.getBytes(StandardCharsets.UTF_8),
        query -> {
          queries.add(query);
          StringBuilder data = new StringBuilder("{");
          Matcher m = FIELD.matcher(query);
          while (m.find()) {
            if (data.length() > 1) {
              data.append(',');
            }
            data.append('"').append(m.group(1)).append("\":{\"words\":[]}");
          }
          return new ByteArrayInputStream(
              data.append('}').toString().getBytes(StandardCharsets.UTF_8));
        });
    // Query, identifier and field0..field500.
    assertThat(queries).hasSize(2);
  }

  private static String words(String name) {
    return switch (name) {
      case "htmlURL" ->
          "{\"words\":[{\"kind\":\"ACRONYM\",\"text\":\"HTML\",\"suffix\":\"\",\"term\":{\"capitalized\":\"Html\"}},"
              + "{\"kind\":\"ACRONYM\",\"text\":\"URL\",\"suffix\":\"\",\"term\":{\"capitalized\":\"Url\"}}]}";
      default ->
          "{\"words\":[{\"kind\":\"WORD\",\"text\":\""
              + name.toLowerCase()
              + "\",\"suffix\":\"\",\"term\":null}]}";
    };
  }

  private static byte[] schema(boolean withIdentifier) {
    String identifier =
        withIdentifier ? "{\"name\":\"identifier\",\"args\":[{\"name\":\"name\"}]}," : "";
    return ("{\"__schema\":{\"queryType\":{\"name\":\"Query\"},\"types\":["
            + "{\"kind\":\"OBJECT\",\"name\":\"Query\",\"fields\":["
            + identifier
            + "{\"name\":\"htmlURL\",\"args\":[]}]},"
            + "{\"kind\":\"ENUM\",\"name\":\"Kind\",\"enumValues\":[{\"name\":\"SHARED\"}]},"
            + "{\"kind\":\"OBJECT\",\"name\":\"__Type\",\"fields\":[{\"name\":\"__typename\"}]},"
            + "{\"kind\":\"SCALAR\",\"name\":\"_\"}"
            + "]}}")
        .getBytes(StandardCharsets.UTF_8);
  }

  private static JsonObject read(byte[] json) {
    try (JsonReader reader =
        Json.createReader(new StringReader(new String(json, StandardCharsets.UTF_8)))) {
      return reader.readObject();
    }
  }
}
