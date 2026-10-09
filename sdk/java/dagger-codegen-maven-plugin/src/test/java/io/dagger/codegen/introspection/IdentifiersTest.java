package io.dagger.codegen.introspection;

import static org.assertj.core.api.Assertions.assertThat;

import io.dagger.codegen.introspection.Identifiers.Casing;
import jakarta.json.bind.Jsonb;
import jakarta.json.bind.JsonbBuilder;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.stream.Stream;
import org.junit.jupiter.api.DynamicTest;
import org.junit.jupiter.api.TestFactory;

/**
 * Checks {@link Identifiers#format} against the engine's shared formatting vectors,
 * engine/naming/testdata/vectors.json.
 */
class IdentifiersTest {

  private static final Path VECTORS = Path.of("engine", "naming", "testdata", "vectors.json");

  public static class Vector {
    public String input;
    public List<IdentifierWord> words;
    public Map<String, String> formats;
  }

  @TestFactory
  Stream<DynamicTest> formatsSharedVectors() throws Exception {
    List<Vector> vectors;
    try (Jsonb jsonb = JsonbBuilder.create()) {
      vectors =
          jsonb.fromJson(
              Files.readString(vectorsPath()),
              new ArrayList<Vector>() {}.getClass().getGenericSuperclass());
    }
    assertThat(vectors).isNotEmpty();
    return vectors.stream()
        .flatMap(
            v ->
                v.formats.entrySet().stream()
                    .map(
                        e ->
                            DynamicTest.dynamicTest(
                                v.input + " in " + e.getKey(),
                                () ->
                                    assertThat(format(v.words, e.getKey()))
                                        .isEqualTo(e.getValue()))));
  }

  private static String format(List<IdentifierWord> words, String casing) {
    boolean capitalized = casing.endsWith("_CAPITALIZED");
    String base =
        capitalized ? casing.substring(0, casing.length() - "_CAPITALIZED".length()) : casing;
    return Identifiers.format(words, Casing.valueOf(base), capitalized);
  }

  /**
   * Finds the vectors: $DAGGER_NAMING_VECTORS, or engine/naming/testdata/vectors.json in the
   * working directory or one of its parents (the tests run from
   * sdk/java/dagger-codegen-maven-plugin in a checkout of the dagger repository).
   */
  private static Path vectorsPath() {
    String env = System.getenv("DAGGER_NAMING_VECTORS");
    if (env != null && !env.isEmpty()) {
      return Path.of(env);
    }
    for (Path dir = Path.of("").toAbsolutePath(); dir != null; dir = dir.getParent()) {
      Path candidate = dir.resolve(VECTORS);
      if (Files.isRegularFile(candidate)) {
        return candidate;
      }
    }
    throw new IllegalStateException(
        "engine/naming/testdata/vectors.json not found above "
            + Path.of("").toAbsolutePath()
            + "; set DAGGER_NAMING_VECTORS to its path");
  }
}
