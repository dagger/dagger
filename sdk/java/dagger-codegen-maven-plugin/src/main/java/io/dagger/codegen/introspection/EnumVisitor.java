package io.dagger.codegen.introspection;

import com.palantir.javapoet.*;
import jakarta.json.bind.annotation.JsonbTypeDeserializer;
import jakarta.json.bind.annotation.JsonbTypeSerializer;
import java.nio.charset.Charset;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import javax.lang.model.SourceVersion;
import javax.lang.model.element.Modifier;

public class EnumVisitor extends AbstractVisitor {

  public EnumVisitor(Schema schema, Path targetDirectory, Charset encoding) {
    super(schema, targetDirectory, encoding);
  }

  @Override
  TypeSpec generateType(Type type) {
    TypeSpec.Builder classBuilder =
        TypeSpec.enumBuilder(Helpers.formatName(type))
            .addJavadoc(Helpers.escapeJavadoc(type.getDescription()))
            .addModifiers(Modifier.PUBLIC);
    Helpers.addGraphQLType(classBuilder, type.getName(), Helpers.formatName(type));

    Map<String, List<EnumValue>> constants = constants(type);
    boolean namedLikeValues =
        constants.entrySet().stream()
            .allMatch(
                e -> e.getValue().size() == 1 && e.getKey().equals(e.getValue().get(0).getName()));
    if (namedLikeValues) {
      // Constants go over the wire by name.
      for (EnumValue enumValue : type.getEnumValues()) {
        TypeSpec.Builder enumTypeBuilder =
            TypeSpec.anonymousClassBuilder("")
                .addJavadoc(Helpers.escapeJavadoc(enumValue.getDescription()));
        if (enumValue.isDeprecated()) {
          enumTypeBuilder.addAnnotation(Deprecated.class);
        }
        classBuilder.addEnumConstant(enumValue.getName(), enumTypeBuilder.build());
      }
      return classBuilder.build();
    }

    return valueEnum(classBuilder, type, constants);
  }

  /**
   * The enum's constants, by name, with the schema values each stands for, in schema order: a
   * value's constant is the name the engine formatted for it ({@code PER_SESSION} for {@code
   * PerSession}), else its schema name. Values whose names format alike, a value and its alias
   * ({@code Gzip} and {@code GZIP}), share a constant, sent as the first one's value.
   */
  private Map<String, List<EnumValue>> constants(Type type) {
    Map<String, List<EnumValue>> constants = new LinkedHashMap<>();
    for (EnumValue enumValue : type.getEnumValues()) {
      String name = enumValue.getName();
      if (getSchema().formatsEnumValues()
          && enumValue.getFormattedName() != null
          && SourceVersion.isName(enumValue.getFormattedName())) {
        name = enumValue.getFormattedName();
      }
      constants.computeIfAbsent(name, k -> new ArrayList<>()).add(enumValue);
    }
    return constants;
  }

  /**
   * An enum whose constants are named apart from their schema values: each carries its values, and
   * the class implements GraphQLEnum, (de)serializing through them. The schema values that used to
   * be constants stay as deprecated aliases.
   */
  private TypeSpec valueEnum(
      TypeSpec.Builder classBuilder, Type type, Map<String, List<EnumValue>> constants) {
    ClassName enumClass = ClassName.bestGuess(Helpers.formatName(type));
    classBuilder
        .addSuperinterface(ClassName.get("io.dagger.client", "GraphQLEnum"))
        .addAnnotation(
            AnnotationSpec.builder(JsonbTypeSerializer.class)
                .addMember(
                    "value", "$T.class", ClassName.get("io.dagger.client", "GraphQLEnumSerializer"))
                .build())
        .addAnnotation(
            AnnotationSpec.builder(JsonbTypeDeserializer.class)
                .addMember(
                    "value",
                    "$T.class",
                    ClassName.get("io.dagger.client", "GraphQLEnumDeserializer"))
                .build());

    for (Map.Entry<String, List<EnumValue>> constant : constants.entrySet()) {
      List<EnumValue> values = constant.getValue();
      EnumValue primary = values.get(0);
      CodeBlock args =
          CodeBlock.join(values.stream().map(v -> CodeBlock.of("$S", v.getName())).toList(), ", ");
      TypeSpec.Builder enumTypeBuilder =
          TypeSpec.anonymousClassBuilder(args)
              .addJavadoc(Helpers.escapeJavadoc(primary.getDescription()));
      if (values.stream().allMatch(EnumValue::isDeprecated)) {
        enumTypeBuilder.addAnnotation(Deprecated.class);
      }
      classBuilder.addEnumConstant(constant.getKey(), enumTypeBuilder.build());
    }

    // The old constants, named like the schema values, as deprecated aliases.
    Set<String> taken = new HashSet<>(constants.keySet());
    for (Map.Entry<String, List<EnumValue>> constant : constants.entrySet()) {
      for (EnumValue value : constant.getValue()) {
        String legacyName = value.getName();
        if (!SourceVersion.isName(legacyName) || !taken.add(legacyName)) {
          continue;
        }
        classBuilder.addField(
            FieldSpec.builder(
                    enumClass, legacyName, Modifier.PUBLIC, Modifier.STATIC, Modifier.FINAL)
                .addAnnotation(Deprecated.class)
                .addJavadoc("@deprecated Use {@link #$L} instead.\n", constant.getKey())
                .initializer("$L", constant.getKey())
                .build());
      }
    }

    classBuilder.addField(
        FieldSpec.builder(String[].class, "values", Modifier.PRIVATE, Modifier.FINAL).build());
    classBuilder.addMethod(
        MethodSpec.constructorBuilder()
            .addParameter(String[].class, "values")
            .varargs()
            .addStatement("this.values = values")
            .build());
    classBuilder.addMethod(
        MethodSpec.methodBuilder("graphQLValue")
            .addAnnotation(Override.class)
            .addModifiers(Modifier.PUBLIC)
            .returns(String.class)
            .addJavadoc("The schema value this constant is sent as.\n")
            .addStatement("return values[0]")
            .build());
    classBuilder.addMethod(
        MethodSpec.methodBuilder("hasGraphQLValue")
            .addAnnotation(Override.class)
            .addModifiers(Modifier.PUBLIC)
            .returns(boolean.class)
            .addParameter(String.class, "value")
            .addJavadoc("Whether the API may return this constant as the given value.\n")
            .beginControlFlow("for (String v : values)")
            .beginControlFlow("if (v.equals(value))")
            .addStatement("return true")
            .endControlFlow()
            .endControlFlow()
            .addStatement("return false")
            .build());
    return classBuilder.build();
  }
}
