package io.dagger.codegen.introspection;

import static org.assertj.core.api.Assertions.assertThat;

import java.io.ByteArrayInputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.util.List;
import org.junit.jupiter.api.Test;

class InterfaceVisitorTest {
  @Test
  void nullableObjectVersionGateHandlesBoundariesAndDevelopmentVersions() throws Exception {
    assertThat(schemaAtVersion(null).supportsNullableObjects()).isTrue();
    assertThat(schemaAtVersion("").supportsNullableObjects()).isTrue();
    assertThat(schemaAtVersion("development").supportsNullableObjects()).isTrue();
    assertThat(schemaAtVersion("v0.21.0-dev").supportsNullableObjects()).isFalse();
    assertThat(schemaAtVersion("v1.0.0-beta.9-dev").supportsNullableObjects()).isFalse();
    assertThat(schemaAtVersion("v1.0.0-beta.10").supportsNullableObjects()).isTrue();
    assertThat(schemaAtVersion("v1.0.0-beta.10-dev").supportsNullableObjects()).isTrue();
    assertThat(schemaAtVersion("v1.0.0-rc.1").supportsNullableObjects()).isTrue();
    assertThat(schemaAtVersion("v1.0.0").supportsNullableObjects()).isTrue();
  }

  @Test
  void optionalObjectMethodsOnlyDeclareExceptionsWhenNullableObjectsAreSupported()
      throws Exception {
    Type type = interfaceWithOptionalObjectField();

    String legacyInterface = generateInterface(type, "v1.0.0-beta.9");
    String nullableInterface = generateInterface(type, "v1.0.0-beta.10");

    assertThat(legacyInterface)
        .contains("Directory child();")
        .doesNotContain("DaggerQueryException");
    assertThat(nullableInterface)
        .contains("Optional<Directory> child()")
        .contains("DaggerQueryException");
  }

  private static String generateInterface(Type type, String version) throws Exception {
    Schema schema = schemaAtVersion(version);
    return new InterfaceVisitor(schema, Path.of("."), StandardCharsets.UTF_8)
        .generateType(type)
        .toString();
  }

  private static Schema schemaAtVersion(String version) throws Exception {
    byte[] introspection = "{\"__schema\":{\"types\":[]}}".getBytes(StandardCharsets.UTF_8);
    return Schema.initialize(new ByteArrayInputStream(introspection), version);
  }

  private static Type interfaceWithOptionalObjectField() {
    TypeRef returnType = new TypeRef();
    returnType.setKind(TypeKind.OBJECT);
    returnType.setName("Directory");

    Type parent = new Type();
    parent.setKind(TypeKind.INTERFACE);
    parent.setName("Parent");
    parent.setDescription("");

    Field field = new Field();
    field.setName("child");
    field.setDescription("");
    field.setTypeRef(returnType);
    field.setArgs(List.of());
    field.setDirectives(List.of());
    field.setParentObject(parent);

    parent.setFields(List.of(field));
    return parent;
  }
}
