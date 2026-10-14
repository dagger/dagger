package io.dagger.codegen.introspection;

import static org.assertj.core.api.Assertions.assertThat;

import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.HashMap;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

/** Renamed core names keep the Java names of their old schema names. */
class RenamedCoreNamesTest {
  @TempDir Path output;

  @Test
  void keepsTheJavaNamesOfRenamedCoreNames() throws Exception {
    Schema schema;
    try (InputStream in = resource("renamed-core-names.json")) {
      schema = Schema.initialize(in, "v1.0.0");
    }
    schema.setFormattedNames(names());
    schema.visit(new CodegenVisitor(schema, output, StandardCharsets.UTF_8));

    // shortSHA is shortSha in Java, as shortSha was: no alias.
    assertThat(read("GitCommit"))
        .contains("public String shortSha()")
        .contains("this.queryBuilder.chain(\"shortSHA\")")
        .doesNotContain("shortSHA()");
    assertThat(read("GitRepository"))
        .contains("public WithRemoteArguments withPushUrl(String pushUrl)")
        .contains("builder.add(\"pushURL\", this.pushUrl)")
        .doesNotContain("withPushURL");
    assertThat(read("LlmContentBlockInput"))
        .contains("@JsonbProperty(\"callID\")")
        .contains("public String getCallId()")
        .contains("map.put(\"callID\", this.callId)")
        .doesNotContain("getCallID")
        .doesNotContain("withCallID");

    // The old values stay as deprecated constants.
    assertThat(read("FunctionCachePolicy"))
        .contains("DEFAULT")
        .contains("PER_SESSION")
        .contains("public static final FunctionCachePolicy Default = DEFAULT;")
        .contains("public static final FunctionCachePolicy PerSession = PER_SESSION;")
        .contains("@deprecated Use {@link #PER_SESSION} instead.");
  }

  private static Map<String, Map<String, String>> names() {
    Map<String, String> types = new HashMap<>();
    types.put("Query", "Query");
    types.put("GitCommit", "GitCommit");
    types.put("GitRepository", "GitRepository");
    types.put("LLMContentBlockInput", "LlmContentBlockInput");
    types.put("FunctionCachePolicy", "FunctionCachePolicy");
    types.put("ID", "Id");
    types.put("String", "String");
    Map<String, String> members = new HashMap<>();
    members.put("gitCommit", "gitCommit");
    members.put("id", "id");
    members.put("shortSHA", "shortSha");
    members.put("withRemote", "withRemote");
    members.put("pushURL", "pushUrl");
    members.put("callID", "callId");
    Map<String, String> values = new HashMap<>();
    values.put("DEFAULT", "DEFAULT");
    values.put("PER_SESSION", "PER_SESSION");
    Map<String, Map<String, String>> names = new HashMap<>();
    names.put(FormattedNames.MEMBERS, members);
    names.put(FormattedNames.TYPES, types);
    names.put(FormattedNames.ENUM_VALUES, values);
    return names;
  }

  private String read(String className) throws Exception {
    return Files.readString(output.resolve("io/dagger/client/" + className + ".java"));
  }

  private static InputStream resource(String name) {
    return RenamedCoreNamesTest.class.getResourceAsStream("/schemas/" + name);
  }
}
