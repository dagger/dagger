package io.dagger.annotation.processor;

import static org.assertj.core.api.Assertions.assertThat;

import io.dagger.client.ImageMediaTypes;
import io.dagger.client.Platform;
import io.dagger.module.info.TypeInfo;
import java.util.HashSet;
import java.util.Set;
import javax.lang.model.type.TypeKind;
import org.junit.jupiter.api.Test;

public class TypeDefTest {
  @Test
  public void testTypeDefString() throws ClassNotFoundException {
    var v = "";
    assertThat(
            DaggerModuleAnnotationProcessor.typeDef(
                    new HashSet<>(),
                    new TypeInfo(v.getClass().getCanonicalName(), TypeKind.DECLARED.name()))
                .toString())
        .isEqualTo(
            "io.dagger.client.Dagger.dag().typeDef().withKind(io.dagger.client.TypeDefKind.STRING_KIND)");
  }

  @Test
  public void testTypeDefInteger() throws ClassNotFoundException {
    var v = Integer.valueOf(1);
    assertThat(
            DaggerModuleAnnotationProcessor.typeDef(
                    new HashSet<>(),
                    new TypeInfo(v.getClass().getCanonicalName(), TypeKind.DECLARED.name()))
                .toString())
        .isEqualTo(
            "io.dagger.client.Dagger.dag().typeDef().withKind(io.dagger.client.TypeDefKind.INTEGER_KIND)");
  }

  @Test
  public void testTypeDefInt() throws ClassNotFoundException {
    assertThat(
            DaggerModuleAnnotationProcessor.typeDef(
                    new HashSet<>(), new TypeInfo("int", TypeKind.INT.name()))
                .toString())
        .isEqualTo(
            "io.dagger.client.Dagger.dag().typeDef().withKind(io.dagger.client.TypeDefKind.INTEGER_KIND)");
  }

  @Test
  public void testTypeDefBool() throws ClassNotFoundException {
    assertThat(
            DaggerModuleAnnotationProcessor.typeDef(
                    new HashSet<>(), new TypeInfo("boolean", TypeKind.BOOLEAN.name()))
                .toString())
        .isEqualTo(
            "io.dagger.client.Dagger.dag().typeDef().withKind(io.dagger.client.TypeDefKind.BOOLEAN_KIND)");
  }

  @Test
  public void testTypeDefVoid() throws ClassNotFoundException {
    assertThat(
            DaggerModuleAnnotationProcessor.typeDef(
                    new HashSet<>(), new TypeInfo("void", TypeKind.VOID.name()))
                .toString())
        .isEqualTo(
            "io.dagger.client.Dagger.dag().typeDef().withKind(io.dagger.client.TypeDefKind.VOID_KIND).withOptional(true)");
  }

  @Test
  public void testTypeDefListString() throws ClassNotFoundException {
    assertThat(
            DaggerModuleAnnotationProcessor.typeDef(
                    new HashSet<>(),
                    new TypeInfo("java.util.List<java.lang.String>", TypeKind.DECLARED.name()))
                .toString())
        .isEqualTo(
            "io.dagger.client.Dagger.dag().typeDef().withListOf(io.dagger.client.Dagger.dag().typeDef().withKind(io.dagger.client.TypeDefKind.STRING_KIND))");
  }

  @Test
  public void testTypeDefListContainer() throws ClassNotFoundException {
    assertThat(
            DaggerModuleAnnotationProcessor.typeDef(
                    new HashSet<>(),
                    new TypeInfo(
                        "java.util.List<io.dagger.client.Container>", TypeKind.DECLARED.name()))
                .toString())
        .isEqualTo(
            "io.dagger.client.Dagger.dag().typeDef().withListOf(io.dagger.client.Dagger.dag().typeDef().withObject(\"Container\"))");
  }

  @Test
  public void testTypeDefEnum() throws ClassNotFoundException {
    var v = ImageMediaTypes.DockerMediaTypes;
    assertThat(
            DaggerModuleAnnotationProcessor.typeDef(
                    new HashSet<>(),
                    new TypeInfo(v.getClass().getCanonicalName(), TypeKind.DECLARED.name()))
                .toString())
        .isEqualTo("io.dagger.client.Dagger.dag().typeDef().withEnum(\"ImageMediaTypes\")");
  }

  @Test
  public void testTypeDefUserDefinedEnum() throws ClassNotFoundException {
    var v = ImageMediaTypes.DockerMediaTypes;
    Set<String> enums = new HashSet<>();
    enums.add("io.dagger.java.module.Severity");
    assertThat(
            DaggerModuleAnnotationProcessor.typeDef(
                    enums, new TypeInfo("io.dagger.java.module.Severity", TypeKind.DECLARED.name()))
                .toString())
        .isEqualTo("io.dagger.client.Dagger.dag().typeDef().withEnum(\"Severity\")");
  }

  @Test
  public void testTypeDefScalar() throws ClassNotFoundException {
    Platform platform = Platform.from("linux/amd64");
    assertThat(
            DaggerModuleAnnotationProcessor.typeDef(
                    new HashSet<>(),
                    new TypeInfo(platform.getClass().getCanonicalName(), TypeKind.DECLARED.name()))
                .toString())
        .isEqualTo("io.dagger.client.Dagger.dag().typeDef().withScalar(\"Platform\")");
  }

  @Test
  public void testTypeDefArray() throws ClassNotFoundException {
    String[] v = {};
    assertThat(
            DaggerModuleAnnotationProcessor.typeDef(
                    new HashSet<>(),
                    new TypeInfo(v.getClass().getCanonicalName(), TypeKind.ARRAY.name()))
                .toString())
        .isEqualTo(
            "io.dagger.client.Dagger.dag().typeDef().withListOf(io.dagger.client.Dagger.dag().typeDef().withKind(io.dagger.client.TypeDefKind.STRING_KIND))");
  }
}
