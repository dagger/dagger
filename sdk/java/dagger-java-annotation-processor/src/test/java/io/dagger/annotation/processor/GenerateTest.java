package io.dagger.annotation.processor;

import static org.assertj.core.api.Assertions.assertThat;

import com.palantir.javapoet.JavaFile;
import io.dagger.module.info.ModuleInfo;
import jakarta.json.bind.Jsonb;
import jakarta.json.bind.JsonbBuilder;
import java.io.BufferedReader;
import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.util.Objects;
import java.util.stream.Collectors;
import org.junit.jupiter.api.Test;

public class GenerateTest {
  @Test
  public void testEntrypointGeneration() throws IOException {
    String moduleJson = getResourceFileAsString("module.json");
    Jsonb jsonb = JsonbBuilder.create();
    ModuleInfo moduleInfo = jsonb.fromJson(moduleJson, ModuleInfo.class);

    JavaFile f = DaggerModuleAnnotationProcessor.generate(moduleInfo);
    // Extension is not .java to avoid to be automatically formatted. We are keeping the default
    // generated code.
    String expected =
        Objects.requireNonNull(getResourceFileAsString("entrypoint.java.expected")).trim();

    assertThat(f.toString().trim()).isEqualTo(expected);
  }

  static String getResourceFileAsString(String fileName) throws IOException {
    ClassLoader classLoader = ClassLoader.getSystemClassLoader();
    try (InputStream is = classLoader.getResourceAsStream(fileName)) {
      if (is == null) return null;
      try (InputStreamReader isr = new InputStreamReader(is);
          BufferedReader reader = new BufferedReader(isr)) {
        return reader.lines().collect(Collectors.joining(System.lineSeparator()));
      }
    }
  }
}
