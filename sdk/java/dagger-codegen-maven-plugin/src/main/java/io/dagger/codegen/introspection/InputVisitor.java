package io.dagger.codegen.introspection;

import com.palantir.javapoet.*;

import javax.lang.model.element.Modifier;
import java.nio.charset.Charset;
import java.nio.file.Path;
import java.util.HashMap;
import java.util.Map;

class InputVisitor extends AbstractVisitor {

  public InputVisitor(Schema schema, Path targetDirectory, Charset encoding) {
    super(schema, targetDirectory, encoding);
  }

  @Override
  TypeSpec generateType(Type type) {
    TypeSpec.Builder classBuilder =
        TypeSpec.classBuilder(Helpers.formatName(type))
            .addJavadoc(type.getDescription())
            .addModifiers(Modifier.PUBLIC)
            .addSuperinterface(ClassName.bestGuess("InputValue"));

    for (InputObject inputObject : type.getInputFields()) {

      classBuilder.addField(
          FieldSpec.builder(
                  inputObject.getType().formatInput(), inputObject.getName(), Modifier.PRIVATE)
              .build());

      classBuilder.addMethod(
          Helpers.getter(inputObject.getName(), inputObject.getType().formatInput()));
      classBuilder.addMethod(
          Helpers.setter(inputObject.getName(), inputObject.getType().formatOutput()));
      classBuilder.addMethod(
          Helpers.withSetter(
              inputObject,
              inputObject.getType().formatInput(),
              ClassName.bestGuess(Helpers.formatName(type))));
    }

    MethodSpec.Builder toMapMethod =
        MethodSpec.methodBuilder("toMap")
            .addModifiers(Modifier.PUBLIC)
            .addAnnotation(Override.class)
            .returns(ParameterizedTypeName.get(Map.class, String.class, Object.class))
            .addStatement(
                "$1T map = new $1T()",
                ParameterizedTypeName.get(HashMap.class, String.class, Object.class));
    for (InputObject inputObject : type.getInputFields()) {
      toMapMethod.beginControlFlow("if (this.$1L != null)", inputObject.getName());
      toMapMethod.addStatement("map.put(\"$1L\", this.$1L)", inputObject.getName());
      toMapMethod.endControlFlow();
    }
    toMapMethod.addStatement("return map");
    classBuilder.addMethod(toMapMethod.build());

    return classBuilder.build();
  }
}
