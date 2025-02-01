package io.dagger.codegen.introspection;

import com.palantir.javapoet.*;
import jakarta.json.bind.Jsonb;
import jakarta.json.bind.JsonbBuilder;
import jakarta.json.bind.JsonbConfig;
import java.nio.charset.Charset;
import java.nio.file.Path;
import java.util.List;
import java.util.stream.Collectors;
import javax.lang.model.element.Modifier;

public class IDAbleVisitor extends AbstractMultiTypesVisitor {
  public IDAbleVisitor(Schema schema, Path targetDirectory, Charset encoding) {
    super(schema, targetDirectory, encoding);
  }

  @Override
  TypeSpec generateType(List<Type> types) {
    TypeSpec.Builder b =
        TypeSpec.classBuilder("JsonConverter")
            .addJavadoc("Convert to and from Json with the right serializers and deserializers")
            .addModifiers(Modifier.PUBLIC, Modifier.FINAL)
            .addMethod(
                MethodSpec.methodBuilder("serializer")
                    .addModifiers(Modifier.PUBLIC, Modifier.STATIC)
                    .returns(Jsonb.class)
                    .addStatement("return $T.create()", JsonbBuilder.class)
                    .build())
            .addMethod(
                MethodSpec.methodBuilder("deserializer")
                    .addModifiers(Modifier.PUBLIC, Modifier.STATIC)
                    .returns(Jsonb.class)
                    .addParameter(ClassName.bestGuess("Client"), "dag")
                    .addStatement("return $T.create(getJsonbConfig(dag))", JsonbBuilder.class)
                    .build())
            .addMethod(
                MethodSpec.methodBuilder("getJsonbConfig")
                    .addModifiers(Modifier.PUBLIC, Modifier.STATIC)
                    .returns(JsonbConfig.class)
                    .addParameter(ClassName.bestGuess("Client"), "dag")
                    .addStatement(
                        "return new $T().withDeserializers($L)",
                        JsonbConfig.class,
                        types.stream()
                            .map(
                                t ->
                                    CodeBlock.of(
                                            "new $T(dag)",
                                            ClassName.bestGuess(t.getName() + ".Deserializer"))
                                        .toString())
                            .collect(Collectors.joining(", ")))
                    .build())
            .addMethod(
                MethodSpec.methodBuilder("toJSON")
                    .addModifiers(Modifier.PUBLIC, Modifier.STATIC)
                    .returns(ClassName.bestGuess("JSON"))
                    .addException(Exception.class)
                    .addParameter(Object.class, "object")
                    .beginControlFlow("try ($T jsonb = serializer())", Jsonb.class)
                    .addStatement(
                        "return $T.from(jsonb.toJson(object))", ClassName.bestGuess("JSON"))
                    .endControlFlow()
                    .build())
            .addMethod(
                MethodSpec.methodBuilder("fromJSON")
                    .addModifiers(Modifier.PUBLIC, Modifier.STATIC)
                    .addTypeVariable(TypeVariableName.get("T"))
                    .returns(TypeVariableName.get("T"))
                    .addParameter(ClassName.bestGuess("Client"), "dag")
                    .addParameter(ClassName.bestGuess("JSON"), "json")
                    .addParameter(
                        ParameterizedTypeName.get(
                            ClassName.get(Class.class), TypeVariableName.get("T")),
                        "clazz")
                    .addException(Exception.class)
                    .addStatement("return fromJSON(dag, json.convert(), clazz)")
                    .build())
            .addMethod(
                MethodSpec.methodBuilder("fromJSON")
                    .addModifiers(Modifier.PUBLIC, Modifier.STATIC)
                    .addTypeVariable(TypeVariableName.get("T"))
                    .returns(TypeVariableName.get("T"))
                    .addParameter(ClassName.bestGuess("Client"), "dag")
                    .addParameter(ClassName.get(String.class), "json")
                    .addParameter(
                        ParameterizedTypeName.get(
                            ClassName.get(Class.class), TypeVariableName.get("T")),
                        "clazz")
                    .addException(Exception.class)
                    .beginControlFlow("try ($T jsonb = deserializer(dag))", Jsonb.class)
                    .addStatement("return jsonb.fromJson(json, clazz)")
                    .endControlFlow()
                    .build());
    return b.build();
  }
}
