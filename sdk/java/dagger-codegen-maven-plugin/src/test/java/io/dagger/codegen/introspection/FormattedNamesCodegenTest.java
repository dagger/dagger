package io.dagger.codegen.introspection;

import static org.assertj.core.api.Assertions.assertThat;

import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

/**
 * Codegen with names the engine formatted (a names file, as {@code codegen introspect --names-out}
 * writes it): Java identifiers follow the formatted names, while everything sent to the API keeps
 * the schema's names.
 */
class FormattedNamesCodegenTest {
  @TempDir Path output;

  @Test
  void namesJavaIdentifiersFromFormattedNames() throws Exception {
    generate(true);

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

    // Enum constants are SCREAMING_SNAKE_CASE, carrying the schema values they go over the wire as.
    // The old constants stay as deprecated aliases.
    assertThat(read("FunctionCachePolicy"))
        .contains("public enum FunctionCachePolicy implements GraphQLEnum")
        .contains("@JsonbTypeSerializer(GraphQLEnumSerializer.class)")
        .contains("@JsonbTypeDeserializer(GraphQLEnumDeserializer.class)")
        .contains("DEFAULT(\"Default\")")
        .contains("PER_SESSION(\"PerSession\")")
        .contains("public static final FunctionCachePolicy PerSession = PER_SESSION;")
        .contains("public static final FunctionCachePolicy Default = DEFAULT;")
        .contains("@deprecated Use {@link #PER_SESSION} instead.")
        .contains("public String graphQLValue()")
        .contains("public boolean hasGraphQLValue(String value)")
        .doesNotContain("@GraphQLType");
    // A value and its alias share a constant, sent as the first value and read back as either.
    assertThat(read("ImageLayerCompression"))
        .contains("GZIP(\"Gzip\", \"GZIP\")")
        .contains("TCP(\"TCP\")")
        .contains("public static final ImageLayerCompression Gzip = GZIP;")
        .doesNotContain("ImageLayerCompression GZIP =")
        .doesNotContain("ImageLayerCompression TCP =");
    // Enums returned by fields are still read by class.
    assertThat(container)
        .contains("public ImageLayerCompression compression()")
        .contains("return nextQueryBuilder.executeQuery(ImageLayerCompression.class)");

    // Classes follow Java's convention, and name their GraphQL type when it differs.
    assertThat(read("JsonValue"))
        .contains("@GraphQLType(\"JSONValue\")")
        .contains("public class JsonValue implements IDAble<Id>")
        .contains("public Json contents()")
        .contains("nodeQueryBuilder(\"JSONValue\", new Id(id))");
    assertThat(read("Json"))
        .contains("@GraphQLType(\"JSON\")")
        .contains("public class Json extends Scalar<String>");
    assertThat(read("Id")).contains("@GraphQLType(\"ID\")").contains("public class Id extends");
    assertThat(read("Container"))
        .contains("public class Container implements Node, IDAble<Id>")
        .doesNotContain("@GraphQLType")
        // Lists of objects instantiate the classes, but select the GraphQL type.
        .contains("public List<JsonValue> values()")
        .contains("nextQueryBuilder.executeObjectListQuery(\"JSONValue\")")
        .contains("new JsonValue(qb)");
    assertThat(read("BuildArg")).doesNotContain("@GraphQLType");
    // Loading by ID reads the class's GraphQL type.
    assertThat(read("Client"))
        .contains("public <T> T loadObjectFromID(Class<T> clazz, Id id)")
        .contains("GraphQLType graphQLType = clazz.getAnnotation(GraphQLType.class);")
        .contains("this.queryBuilder.chainNode(typeName, id)")
        .doesNotContain("@GraphQLType");
    // JSON conversions go through the enums' values.
    assertThat(read("JsonConverter"))
        .contains("public static Json toJSON(Object object)")
        .contains("GraphQLEnums.value((Enum<?>) object)")
        .contains("GraphQLEnums.valueOf(clazz, jsonb.fromJson(json, String.class))");
  }

  @Test
  void keepsSchemaNamesWithoutFormattedNames() throws Exception {
    generate(false);

    assertThat(read("Container"))
        .contains("public String htmlURL()")
        .contains("public String asJSON()")
        .contains("public List<String> prerequisiteSHAs()")
        .contains("public Container withGPU(List<String> devices)")
        .contains("public Container withMCPServer(String name, WithMCPServerArguments optArgs)")
        .contains(
            "public WithMCPServerArguments withInsecureSkipTLSVerify(Boolean insecureSkipTLSVerify)")
        .contains("public Container importTarball(String source)")
        .contains("public class Container implements Node, IDAble<ID>")
        .contains("public List<JSONValue> values()")
        .contains("new JSONValue(qb)")
        .doesNotContain("htmlUrl")
        .doesNotContain("@Deprecated");
    assertThat(read("Node")).contains("String htmlURL() throws").doesNotContain("default");
    assertThat(read("BuildArg"))
        .contains("private Boolean insecureSkipTLSVerify;")
        .contains("map.put(\"insecureSkipTLSVerify\", this.insecureSkipTLSVerify)")
        .doesNotContain("JsonbProperty")
        .doesNotContain("@Deprecated");
    // Classes and enum constants keep the schema's names.
    assertThat(read("JSONValue")).contains("public class JSONValue").doesNotContain("GraphQLType");
    assertThat(read("JSON")).contains("public class JSON").doesNotContain("GraphQLType");
    assertThat(read("ID")).contains("public class ID").doesNotContain("GraphQLType");
    assertThat(read("FunctionCachePolicy"))
        .contains("PerSession")
        .doesNotContain("PER_SESSION")
        .doesNotContain("GraphQLEnum");
    assertThat(read("ImageLayerCompression")).contains("Gzip,").contains("GZIP");
    assertThat(read("Client"))
        .contains("this.queryBuilder.chainNode(clazz.getSimpleName(), id)")
        .doesNotContain("GraphQLType");
    assertThat(read("JsonConverter"))
        .contains("public static JSON toJSON(Object object)")
        .doesNotContain("GraphQLEnums");
    assertThat(output.resolve("io/dagger/client/Json.java")).doesNotExist();
  }

  @Test
  void keepsSchemaNamesMissingFromTheNames() throws Exception {
    Map<String, Map<String, String>> names = names();
    names.get(FormattedNames.MEMBERS).remove("withMCPServer");
    names.get(FormattedNames.TYPES).remove("JSONValue");
    names.get(FormattedNames.ENUM_VALUES).remove("PerSession");
    generate(names);

    // Its argument is still renamed.
    assertThat(read("Container"))
        .contains("public Container withMCPServer(String name)")
        .contains("public static class WithMCPServerArguments")
        .contains("private Boolean insecureSkipTlsVerify;")
        .contains("public Container withGpu(List<String> devices)")
        .contains("public List<JSONValue> values()")
        .doesNotContain("withMcpServer");
    assertThat(read("JSONValue")).doesNotContain("GraphQLType").contains("IDAble<Id>");
    assertThat(read("FunctionCachePolicy"))
        .contains("DEFAULT(\"Default\")")
        .contains("PerSession(\"PerSession\")")
        .doesNotContain("PER_SESSION");
  }

  @Test
  void keepsTheFormatsMissingFromTheNames() throws Exception {
    // A names file with member names only (as written before class and constant names were
    // formatted) keeps the schema's class names and enum constants.
    Map<String, Map<String, String>> names = names();
    names.remove(FormattedNames.TYPES);
    names.remove(FormattedNames.ENUM_VALUES);
    generate(names);

    assertThat(read("Container"))
        .contains("public String htmlUrl()")
        .contains("public class Container implements Node, IDAble<ID>");
    assertThat(read("JSONValue")).doesNotContain("GraphQLType");
    assertThat(read("FunctionCachePolicy"))
        .contains("Default,")
        .contains("PerSession\n")
        .doesNotContain("GraphQLEnum");
  }

  private void generate(boolean withNames) throws Exception {
    generate(withNames ? names() : null);
  }

  private void generate(Map<String, Map<String, String>> names) throws Exception {
    Schema schema;
    try (InputStream in = resource("formatted-names.json")) {
      schema = Schema.initialize(in, "v1.0.0");
    }
    schema.setFormattedNames(names);
    schema.visit(new CodegenVisitor(schema, output, StandardCharsets.UTF_8));
  }

  private static Map<String, Map<String, String>> names() throws Exception {
    try (InputStream in = resource("formatted-names.names.json")) {
      return FormattedNames.read(in);
    }
  }

  private String read(String className) throws Exception {
    return Files.readString(output.resolve("io/dagger/client/" + className + ".java"));
  }

  private static InputStream resource(String name) {
    return FormattedNamesCodegenTest.class.getResourceAsStream("/schemas/" + name);
  }
}
