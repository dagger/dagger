package io.dagger.codegen.introspection;

import static org.assertj.core.api.Assertions.assertThat;

import java.io.ByteArrayInputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class ObjectMethodNamesTest {
  @TempDir Path output;

  @Test
  void escapesObjectMethodsButPreservesGraphqlFieldNames() throws Exception {
    Type agent = agent();
    List<String> names =
        List.of(
            "wait", "notify", "notifyAll", "getClass", "hashCode", "toString", "clone", "finalize");
    agent.setFields(names.stream().map(name -> field(agent, name, List.of())).toList());

    String generated =
        new ObjectVisitor(schema(), output, StandardCharsets.UTF_8).generateType(agent).toString();

    for (String name : names) {
      assertThat(generated)
          .contains("Agent " + name + "_()")
          .doesNotContain("Agent " + name + "()")
          .contains("this.queryBuilder.chain(\"" + name + "\")");
    }
  }

  @Test
  void escapesOptionalOverloadsButPreservesRequiredArgumentOverloads() throws Exception {
    Type agent = agent();
    agent.setFields(
        List.of(
            field(agent, "wait", List.of(argument("timeout", false))),
            field(agent, "notify", List.of(argument("subscriber", true)))));

    String generated =
        new ObjectVisitor(schema(), output, StandardCharsets.UTF_8).generateType(agent).toString();

    assertThat(generated)
        .contains("Agent wait_()")
        .contains("Agent wait_(Wait_Arguments optArgs)")
        .contains("this.queryBuilder.chain(\"wait\", fieldArgs)")
        .contains("Agent notify(java.lang.String subscriber)")
        .contains("this.queryBuilder.chain(\"notify\", fieldArgs)")
        .doesNotContain("Agent notify_(");
  }

  @Test
  void escapesInterfaceAndClientMethodsConsistently() throws Exception {
    Type agent = agent();
    agent.setKind(TypeKind.INTERFACE);
    agent.setFields(List.of(field(agent, "wait", List.of())));

    new InterfaceVisitor(schema(), output, StandardCharsets.UTF_8).visit(agent);

    assertThat(Files.readString(output.resolve("io/dagger/client/Agent.java")))
        .contains("Agent wait_()");
    assertThat(Files.readString(output.resolve("io/dagger/client/AgentClient.java")))
        .contains("Agent wait_()")
        .contains("this.queryBuilder.chain(\"wait\")");
  }

  private static Schema schema() throws Exception {
    byte[] introspection = "{\"__schema\":{\"types\":[]}}".getBytes(StandardCharsets.UTF_8);
    return Schema.initialize(new ByteArrayInputStream(introspection), "v1.0.0");
  }

  private static Type agent() {
    Type type = new Type();
    type.setName("Agent");
    type.setKind(TypeKind.OBJECT);
    type.setDescription("");
    return type;
  }

  private static Field field(Type parent, String name, List<InputObject> args) {
    TypeRef id = new TypeRef();
    id.setKind(TypeKind.SCALAR);
    id.setName("ID");
    TypeRef returnType = new TypeRef();
    returnType.setKind(TypeKind.NON_NULL);
    returnType.setOfType(id);

    DirectiveArg expectedName = new DirectiveArg();
    expectedName.setName("name");
    expectedName.setValue("\"Agent\"");
    Directive expected = new Directive();
    expected.setName("expectedType");
    expected.setArgs(List.of(expectedName));

    Field field = new Field();
    field.setName(name);
    field.setDescription("");
    field.setParentObject(parent);
    field.setTypeRef(returnType);
    field.setDirectives(List.of(expected));
    field.setArgs(args);
    return field;
  }

  private static InputObject argument(String name, boolean required) {
    TypeRef type = new TypeRef();
    type.setKind(TypeKind.SCALAR);
    type.setName("String");
    if (required) {
      TypeRef nonNull = new TypeRef();
      nonNull.setKind(TypeKind.NON_NULL);
      nonNull.setOfType(type);
      type = nonNull;
    }
    InputObject arg = new InputObject();
    arg.setName(name);
    arg.setDescription("");
    arg.setType(type);
    arg.setDirectives(List.of());
    return arg;
  }
}
