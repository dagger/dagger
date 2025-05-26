package io.dagger.annotation.processor;

import static com.google.testing.compile.CompilationSubject.assertThat;
import static com.google.testing.compile.Compiler.javac;

import com.google.testing.compile.Compilation;
import com.google.testing.compile.JavaFileObjects;
import org.junit.jupiter.api.Test;
import uk.org.webcompere.systemstubs.environment.EnvironmentVariables;

public class GenerateTest {
  @Test
  public void testRuntimeGeneration() throws Exception {
    new EnvironmentVariables("_DAGGER_JAVA_SDK_MODULE_NAME", "dagger-java")
        .execute(
            () -> {
              Compilation compilation =
                  javac()
                      .withProcessors(new DaggerModuleAnnotationProcessor())
                      .compile(
                          JavaFileObjects.forResource("io/dagger/java/module/DaggerJava.java"),
                          JavaFileObjects.forResource("io/dagger/java/module/package-info.java"));
              assertThat(compilation).succeeded();
              assertThat(compilation)
                  .generatedSourceFile("io.dagger.gen.entrypoint.Entrypoint")
                  .hasSourceEquivalentTo(
                      JavaFileObjects.forResource("io/dagger/gen/entrypoint/Entrypoint.java"));
            });
  }

  @Test
  public void testTypeDefsGeneration() throws Exception {
    new EnvironmentVariables("_DAGGER_JAVA_SDK_MODULE_NAME", "dagger-java")
        .execute(
            () -> {
              Compilation compilation =
                  javac()
                      .withProcessors(new TypeDefs())
                      .compile(
                          JavaFileObjects.forResource("io/dagger/java/module/DaggerJava.java"),
                          JavaFileObjects.forResource("io/dagger/java/module/package-info.java"));
              assertThat(compilation).succeeded();
              assertThat(compilation)
                  .generatedSourceFile("io.dagger.gen.entrypoint.TypeDefs")
                  .hasSourceEquivalentTo(
                      JavaFileObjects.forResource("io/dagger/gen/entrypoint/typedefs.java"));
            });
  }
}
