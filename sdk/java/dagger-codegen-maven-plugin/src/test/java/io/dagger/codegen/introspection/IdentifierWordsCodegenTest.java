package io.dagger.codegen.introspection;

import static org.assertj.core.api.Assertions.assertThat;

import jakarta.json.Json;
import jakarta.json.JsonObject;
import jakarta.json.JsonReader;
import java.io.ByteArrayInputStream;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

/**
 * Codegen from a schema JSON with identifier words (__identifiers): Java identifiers follow the
 * words, while everything sent to the API keeps the schema's names.
 */
class IdentifierWordsCodegenTest {
  @TempDir Path output;

  @Test
  void namesJavaIdentifiersFromWords() throws Exception {
    generate(fixture(true));

    String container = read("Container");
    // Methods are camelCase with acronyms written like words, and select the schema's fields.
    assertThat(container)
        .contains("public String htmlUrl()")
        .contains("this.queryBuilder.chain(\"htmlURL\")")
        .contains("public String asJson()")
        .contains("this.queryBuilder.chain(\"asJSON\")")
        .contains("public List<String> prerequisiteShas()")
        .contains("public Container withGpu(List<String> devices)")
        .contains("builder.add(\"devices\", devices)")
        .contains("this.queryBuilder.chain(\"withGPU\", fieldArgs)")
        .contains("public Container withMcpServer(String name)")
        .contains("public Container withMcpServer(String name, WithMCPServerArguments optArgs)")
        .contains("builder.add(\"name\", name)")
        .contains("this.queryBuilder.chain(\"withMCPServer\", fieldArgs)")
        .contains("public Container importTarball(String source)")
        .contains("this.queryBuilder.chain(\"import\", fieldArgs)");
    // Optional arguments keep their class, and send the schema's argument names.
    assertThat(container)
        .contains("public static class WithMCPServerArguments")
        .contains("private Boolean insecureSkipTlsVerify;")
        .contains(
            "public WithMCPServerArguments withInsecureSkipTlsVerify(Boolean insecureSkipTlsVerify)")
        .contains("builder.add(\"insecureSkipTLSVerify\", this.insecureSkipTlsVerify)");
    // The old names stay as deprecated aliases.
    assertThat(container)
        .contains("public String htmlURL()")
        .contains("return htmlUrl();")
        .contains("public String asJSON()")
        .contains("return asJson();")
        .contains("public List<String> prerequisiteSHAs()")
        .contains("public Container withGPU(List<String> devices)")
        .contains("return withGpu(devices);")
        .contains("public Container withMCPServer(String name)")
        .contains("public Container withMCPServer(String name, WithMCPServerArguments optArgs)")
        .contains("return withMcpServer(name, optArgs);")
        .contains(
            "public WithMCPServerArguments withInsecureSkipTLSVerify(Boolean insecureSkipTlsVerify)")
        .contains("return withInsecureSkipTlsVerify(insecureSkipTlsVerify);")
        .contains("@deprecated Use {@link #withMcpServer} instead.")
        .doesNotContain("importTarball_")
        .doesNotContain(" import_(");

    // Interfaces declare the new name, and keep the old one as a default method.
    assertThat(read("Node"))
        .contains("String htmlUrl() throws")
        .contains("default String htmlURL() throws")
        .contains("return htmlUrl();");
    assertThat(read("NodeClient"))
        .contains("public String htmlUrl()")
        .contains("this.queryBuilder.chain(\"htmlURL\")")
        .doesNotContain("String htmlURL()");

    // Input objects send and serialize the schema's field names.
    assertThat(read("BuildArg"))
        .contains("@JsonbProperty(\"insecureSkipTLSVerify\")")
        .contains("private Boolean insecureSkipTlsVerify;")
        .contains("public Boolean isInsecureSkipTlsVerify()")
        .contains("public void setInsecureSkipTlsVerify(Boolean insecureSkipTlsVerify)")
        .contains("public BuildArg withInsecureSkipTlsVerify(Boolean insecureSkipTlsVerify)")
        .contains("map.put(\"insecureSkipTLSVerify\", this.insecureSkipTlsVerify)")
        .contains("public Boolean isInsecureSkipTLSVerify()")
        .contains("public void setInsecureSkipTLSVerify(Boolean insecureSkipTlsVerify)")
        .contains("public BuildArg withInsecureSkipTLSVerify(Boolean insecureSkipTlsVerify)")
        .contains("private String name;")
        .doesNotContain("@JsonbProperty(\"name\")");

    // Enum constants are serialized by name: they keep the schema's values.
    assertThat(read("FunctionCachePolicy")).contains("PerSession").doesNotContain("PER_SESSION");
  }

  @Test
  void keepsSchemaNamesWithoutWords() throws Exception {
    generate(fixture(false));

    assertThat(read("Container"))
        .contains("public String htmlURL()")
        .contains("public String asJSON()")
        .contains("public List<String> prerequisiteSHAs()")
        .contains("public Container withGPU(List<String> devices)")
        .contains("public Container withMCPServer(String name, WithMCPServerArguments optArgs)")
        .contains(
            "public WithMCPServerArguments withInsecureSkipTLSVerify(Boolean insecureSkipTLSVerify)")
        .contains("public Container importTarball(String source)")
        .doesNotContain("htmlUrl")
        .doesNotContain("@Deprecated");
    assertThat(read("Node")).contains("String htmlURL() throws").doesNotContain("default");
    assertThat(read("BuildArg"))
        .contains("private Boolean insecureSkipTLSVerify;")
        .contains("map.put(\"insecureSkipTLSVerify\", this.insecureSkipTLSVerify)")
        .doesNotContain("JsonbProperty")
        .doesNotContain("@Deprecated");
  }

  private void generate(String json) throws Exception {
    Schema schema =
        Schema.initialize(
            new ByteArrayInputStream(json.getBytes(StandardCharsets.UTF_8)), "v1.0.0");
    schema.visit(new CodegenVisitor(schema, output, StandardCharsets.UTF_8));
  }

  private String read(String className) throws Exception {
    return Files.readString(output.resolve("io/dagger/client/" + className + ".java"));
  }

  private static String fixture(boolean withWords) throws Exception {
    try (InputStream in =
            IdentifierWordsCodegenTest.class.getResourceAsStream("/schemas/identifier-words.json");
        JsonReader reader = Json.createReader(in)) {
      JsonObject json = reader.readObject();
      if (!withWords) {
        json = Json.createObjectBuilder(json).remove("__identifiers").build();
      }
      return json.toString();
    }
  }
}
