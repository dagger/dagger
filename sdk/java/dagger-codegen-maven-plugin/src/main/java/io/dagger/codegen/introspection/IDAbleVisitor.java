package io.dagger.codegen.introspection;

import com.palantir.javapoet.*;
import jakarta.json.bind.Jsonb;
import jakarta.json.bind.JsonbBuilder;
import jakarta.json.bind.JsonbConfig;
import java.lang.reflect.Method;
import java.nio.charset.Charset;
import java.nio.file.Path;
import java.util.List;
import javax.lang.model.element.Modifier;

public class IDAbleVisitor extends AbstractMultiTypesVisitor {
  public IDAbleVisitor(Schema schema, Path targetDirectory, Charset encoding) {
    super(schema, targetDirectory, encoding);
  }

  @Override
  TypeSpec generateType(List<Type> types) {
    ClassName json = Helpers.className("JSON");
    // Enums named apart from their schema values convert through their values; enums named like
    // them, through their names (see GraphQLEnums).
    boolean enumValues = getSchema().formatsEnumValues();
    ClassName graphQLEnums = ClassName.get("io.dagger.client", "GraphQLEnums");

    MethodSpec.Builder toJSON =
        MethodSpec.methodBuilder("toJSON")
            .addModifiers(Modifier.PUBLIC, Modifier.STATIC)
            .returns(json)
            .addException(Exception.class)
            .addParameter(Object.class, "object")
            .beginControlFlow(
                "try ($T jsonb = $T.create(new $T().withPropertyVisibilityStrategy(new $T())))",
                Jsonb.class,
                JsonbBuilder.class,
                JsonbConfig.class,
                ClassName.bestGuess("io.dagger.client.FieldsStrategy"))
            .beginControlFlow("if (object instanceof $T<?>)", Enum.class);
    if (enumValues) {
      toJSON.addStatement(
          "return $T.from(jsonb.toJson($T.value(($T<?>) object)))", json, graphQLEnums, Enum.class);
    } else {
      toJSON.addStatement(
          "return $T.from(jsonb.toJson((($T<?>) object).name()))", json, Enum.class);
    }
    toJSON
        .endControlFlow()
        .addStatement("return $T.from(jsonb.toJson(object))", json)
        .endControlFlow();

    MethodSpec.Builder fromJSON =
        MethodSpec.methodBuilder("fromJSON")
            .addModifiers(Modifier.PUBLIC, Modifier.STATIC)
            .addTypeVariable(TypeVariableName.get("T"))
            .returns(TypeVariableName.get("T"))
            .addParameter(ClassName.get(String.class), "json")
            .addParameter(
                ParameterizedTypeName.get(ClassName.get(Class.class), TypeVariableName.get("T")),
                "clazz")
            .addException(Exception.class)
            .beginControlFlow(
                "try ($T jsonb = $T.create(new $T().withPropertyVisibilityStrategy(new $T())))",
                Jsonb.class,
                JsonbBuilder.class,
                JsonbConfig.class,
                ClassName.bestGuess("io.dagger.client.FieldsStrategy"))
            .beginControlFlow("if (clazz.isEnum())");
    if (enumValues) {
      fromJSON.addStatement(
          "return $T.valueOf(clazz, jsonb.fromJson(json, $T.class))", graphQLEnums, String.class);
    } else {
      fromJSON
          .addStatement(
              "$T valueOf = clazz.getMethod($S, $T.class)", Method.class, "valueOf", String.class)
          .addStatement(
              "return clazz.cast(valueOf.invoke(null, jsonb.fromJson(json, $T.class)))",
              String.class);
    }
    fromJSON.endControlFlow().addStatement("return jsonb.fromJson(json, clazz)").endControlFlow();

    TypeSpec.Builder b =
        TypeSpec.classBuilder("JsonConverter")
            .addJavadoc("Convert to and from Json with the right serializers and deserializers")
            .addModifiers(Modifier.PUBLIC, Modifier.FINAL)
            .addMethod(toJSON.build())
            .addMethod(
                MethodSpec.methodBuilder("fromJSON")
                    .addModifiers(Modifier.PUBLIC, Modifier.STATIC)
                    .addTypeVariable(TypeVariableName.get("T"))
                    .returns(TypeVariableName.get("T"))
                    .addParameter(json, "json")
                    .addParameter(
                        ParameterizedTypeName.get(
                            ClassName.get(Class.class), TypeVariableName.get("T")),
                        "clazz")
                    .addException(Exception.class)
                    .addStatement("return fromJSON(json.convert(), clazz)")
                    .build())
            .addMethod(fromJSON.build());
    return b.build();
  }
}
