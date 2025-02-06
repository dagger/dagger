package io.dagger.annotation.processor;

import static org.assertj.core.api.Assertions.assertThat;

import io.dagger.client.ImageMediaTypes;
import io.dagger.module.info.TypeInfo;
import javax.lang.model.type.TypeKind;
import org.junit.jupiter.api.Test;

public class TypeDefTest {
  @Test
  public void testTypeDefString() throws ClassNotFoundException {
    var v = "";
    assertThat(
            DaggerModuleAnnotationProcessor.typeDef(
                    new TypeInfo(v.getClass().getCanonicalName(), TypeKind.DECLARED.name()))
                .toString())
        .isEqualTo("dag.typeDef().withKind(io.dagger.client.TypeDefKind.STRING_KIND)");
  }

  @Test
  public void testTypeDefInteger() throws ClassNotFoundException {
    var v = Integer.valueOf(1);
    assertThat(
            DaggerModuleAnnotationProcessor.typeDef(
                    new TypeInfo(v.getClass().getCanonicalName(), TypeKind.DECLARED.name()))
                .toString())
        .isEqualTo("dag.typeDef().withKind(io.dagger.client.TypeDefKind.INTEGER_KIND)");
  }

  @Test
  public void testTypeDefInt() throws ClassNotFoundException {
    assertThat(
            DaggerModuleAnnotationProcessor.typeDef(new TypeInfo("int", TypeKind.INT.name()))
                .toString())
        .isEqualTo("dag.typeDef().withKind(io.dagger.client.TypeDefKind.INTEGER_KIND)");
  }

  @Test
  public void testTypeDefBool() throws ClassNotFoundException {
    assertThat(
            DaggerModuleAnnotationProcessor.typeDef(
                    new TypeInfo("boolean", TypeKind.BOOLEAN.name()))
                .toString())
        .isEqualTo("dag.typeDef().withKind(io.dagger.client.TypeDefKind.BOOLEAN_KIND)");
  }

  @Test
  public void testTypeDefListString() throws ClassNotFoundException {
    assertThat(
            DaggerModuleAnnotationProcessor.typeDef(
                    new TypeInfo("java.util.List<java.lang.String>", TypeKind.DECLARED.name()))
                .toString())
        .isEqualTo(
            "dag.typeDef().withListOf(dag.typeDef().withKind(io.dagger.client.TypeDefKind.STRING_KIND))");
  }

  @Test
  public void testTypeDefListContainer() throws ClassNotFoundException {
    assertThat(
            DaggerModuleAnnotationProcessor.typeDef(
                    new TypeInfo(
                        "java.util.List<io.dagger.client.Container>", TypeKind.DECLARED.name()))
                .toString())
        .isEqualTo("dag.typeDef().withListOf(dag.typeDef().withObject(\"Container\"))");
  }

  @Test
  public void testTypeDefEnum() throws ClassNotFoundException {
    var v = ImageMediaTypes.DockerMediaTypes;
    assertThat(
            DaggerModuleAnnotationProcessor.typeDef(
                    new TypeInfo(v.getClass().getCanonicalName(), TypeKind.DECLARED.name()))
                .toString())
        .isEqualTo("dag.typeDef().withEnum(\"ImageMediaTypes\")");
  }

  @Test
  public void testTypeDefArray() throws ClassNotFoundException {
    String[] v = {};
    assertThat(
            DaggerModuleAnnotationProcessor.typeDef(
                    new TypeInfo(v.getClass().getCanonicalName(), TypeKind.ARRAY.name()))
                .toString())
        .isEqualTo(
            "dag.typeDef().withListOf(dag.typeDef().withKind(io.dagger.client.TypeDefKind.STRING_KIND))");
  }
}
