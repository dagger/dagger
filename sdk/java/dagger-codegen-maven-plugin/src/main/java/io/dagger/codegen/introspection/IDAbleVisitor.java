package io.dagger.codegen.introspection;

import com.palantir.javapoet.*;
import jakarta.json.bind.Jsonb;
import jakarta.json.bind.JsonbBuilder;
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
    TypeSpec.Builder b =
        TypeSpec.classBuilder("JsonConverter")
            .addJavadoc("Convert to and from Json with the right serializers and deserializers")
            .addModifiers(Modifier.PUBLIC, Modifier.FINAL)
            .addMethod(
                MethodSpec.methodBuilder("toJSON")
                    .addModifiers(Modifier.PUBLIC, Modifier.STATIC)
                    .returns(ClassName.bestGuess("JSON"))
                    .addException(Exception.class)
                    .addParameter(Object.class, "object")
                    .beginControlFlow(
                        "try ($T jsonb = $T.create())", Jsonb.class, JsonbBuilder.class)
                    .addStatement(
                        "return $T.from(jsonb.toJson(object))", ClassName.bestGuess("JSON"))
                    .endControlFlow()
                    .build())
            .addMethod(
                MethodSpec.methodBuilder("fromJSON")
                    .addModifiers(Modifier.PUBLIC, Modifier.STATIC)
                    .addTypeVariable(TypeVariableName.get("T"))
                    .returns(TypeVariableName.get("T"))
                    .addParameter(ClassName.bestGuess("JSON"), "json")
                    .addParameter(
                        ParameterizedTypeName.get(
                            ClassName.get(Class.class), TypeVariableName.get("T")),
                        "clazz")
                    .addException(Exception.class)
                    .addStatement("return fromJSON(json.convert(), clazz)")
                    .build())
            .addMethod(
                MethodSpec.methodBuilder("fromJSON")
                    .addModifiers(Modifier.PUBLIC, Modifier.STATIC)
                    .addTypeVariable(TypeVariableName.get("T"))
                    .returns(TypeVariableName.get("T"))
                    .addParameter(ClassName.get(String.class), "json")
                    .addParameter(
                        ParameterizedTypeName.get(
                            ClassName.get(Class.class), TypeVariableName.get("T")),
                        "clazz")
                    .addException(Exception.class)
                    .beginControlFlow(
                        "try ($T jsonb = $T.create())", Jsonb.class, JsonbBuilder.class)
                    .addStatement("return jsonb.fromJson(json, clazz)")
                    .endControlFlow()
                    .build());
    return b.build();
  }
}
