package io.dagger.annotation.processor;

import static com.google.testing.compile.CompilationSubject.assertThat;
import static com.google.testing.compile.Compiler.javac;

import com.google.testing.compile.Compilation;
import com.google.testing.compile.JavaFileObjects;
import javax.tools.JavaFileObject;
import org.junit.jupiter.api.Test;
import org.assertj.core.api.Assertions;
import uk.org.webcompere.systemstubs.environment.EnvironmentVariables;

public class CollectionGenerateTest {
  @Test
  public void testCollectionAnnotationGeneration() throws Exception {
    new EnvironmentVariables("_DAGGER_JAVA_SDK_MODULE_NAME", "collections")
        .execute(
            () -> {
              Compilation compilation =
                  javac()
                      .withProcessors(new DaggerModuleAnnotationProcessor())
                      .compile(
                          JavaFileObjects.forResource("io/dagger/java/collection/Collections.java"),
                          JavaFileObjects.forResource("io/dagger/java/collection/GoTest.java"));
              assertThat(compilation).succeeded();
              JavaFileObject generated =
                  compilation
                      .generatedSourceFile("io.dagger.gen.entrypoint.Entrypoint")
                      .orElseThrow();
              String source = generated.getCharContent(false).toString();
              Assertions.assertThat(source).contains(".withCollection()");
              Assertions.assertThat(source).contains(".withCollectionKeys(\"paths\")");
              Assertions.assertThat(source).contains(".withCollectionGet(\"module\")");
            });
  }
}
