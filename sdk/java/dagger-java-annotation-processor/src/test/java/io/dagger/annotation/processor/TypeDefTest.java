package io.dagger.annotation.processor;

import static org.assertj.core.api.Assertions.assertThat;

import io.dagger.client.ImageMediaTypes;
import org.junit.jupiter.api.Test;

public class TypeDefTest {
  @Test
  public void testTypeDefString() throws ClassNotFoundException {
    var v = "";
    assertThat(DaggerModuleAnnotationProcessor.typeDef(v.getClass().getCanonicalName()).toString())
        .isEqualTo("dag.typeDef().withKind(io.dagger.client.TypeDefKind.STRING_KIND)");
  }

  @Test
  public void testTypeDefInteger() throws ClassNotFoundException {
    var v = Integer.valueOf(1);
    assertThat(DaggerModuleAnnotationProcessor.typeDef(v.getClass().getCanonicalName()).toString())
        .isEqualTo("dag.typeDef().withKind(io.dagger.client.TypeDefKind.INTEGER_KIND)");
  }

  @Test
  public void testTypeDefInt() throws ClassNotFoundException {
    assertThat(DaggerModuleAnnotationProcessor.typeDef("int").toString())
        .isEqualTo("dag.typeDef().withKind(io.dagger.client.TypeDefKind.INTEGER_KIND)");
  }

  @Test
  public void testTypeDefBool() throws ClassNotFoundException {
    assertThat(DaggerModuleAnnotationProcessor.typeDef("boolean").toString())
        .isEqualTo("dag.typeDef().withKind(io.dagger.client.TypeDefKind.BOOLEAN_KIND)");
  }

  @Test
  public void testTypeDefListString() throws ClassNotFoundException {
    assertThat(
            DaggerModuleAnnotationProcessor.typeDef("java.util.List<java.lang.String>").toString())
        .isEqualTo(
            "dag.typeDef().withListOf(dag.typeDef().withKind(io.dagger.client.TypeDefKind.STRING_KIND))");
  }

  @Test
  public void testTypeDefListContainer() throws ClassNotFoundException {
    assertThat(
            DaggerModuleAnnotationProcessor.typeDef("java.util.List<io.dagger.client.Container>")
                .toString())
        .isEqualTo("dag.typeDef().withListOf(dag.typeDef().withObject(\"Container\"))");
  }

  @Test
  public void testTypeDefEnum() throws ClassNotFoundException {
    var v = ImageMediaTypes.DockerMediaTypes;
    assertThat(DaggerModuleAnnotationProcessor.typeDef(v.getClass().getCanonicalName()).toString())
        .isEqualTo("dag.typeDef().withEnum(\"ImageMediaTypes\")");
  }

  @Test
  public void testTypeDefArray() throws ClassNotFoundException {
    String[] v = {};
    assertThat(DaggerModuleAnnotationProcessor.typeDef(v.getClass().getCanonicalName()).toString())
        .isEqualTo(
            "dag.typeDef().withListOf(dag.typeDef().withKind(io.dagger.client.TypeDefKind.STRING_KIND))");
  }
}
