package io.dagger.annotation.processor;

import static com.google.testing.compile.CompilationSubject.assertThat;
import static com.google.testing.compile.Compiler.javac;

import com.google.testing.compile.Compilation;
import com.google.testing.compile.JavaFileObjects;
import java.io.IOException;
import org.junit.jupiter.api.Test;

public class GenerateTest {
  @Test
  public void testAnnotationGeneration() throws IOException {
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
            JavaFileObjects.forResource("io/dagger/gen/entrypoint/entrypoint.java"));
  }
}
