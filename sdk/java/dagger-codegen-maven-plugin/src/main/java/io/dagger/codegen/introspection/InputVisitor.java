package io.dagger.codegen.introspection;

import com.palantir.javapoet.*;
import jakarta.json.bind.annotation.JsonbProperty;
import java.nio.charset.Charset;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.stream.Collectors;
import javax.lang.model.element.Modifier;

class InputVisitor extends AbstractVisitor {

  public InputVisitor(Schema schema, Path targetDirectory, Charset encoding) {
    super(schema, targetDirectory, encoding);
  }

  @Override
  TypeSpec generateType(Type type) {
    TypeSpec.Builder classBuilder =
        TypeSpec.classBuilder(Helpers.formatName(type))
            .addJavadoc(Helpers.escapeJavadoc(type.getDescription()))
            .addModifiers(Modifier.PUBLIC)
            .addSuperinterface(ClassName.bestGuess("InputValue"));

    List<MethodSpec> methods = new ArrayList<>();
    List<String> legacyNames = new ArrayList<>();
    for (InputObject inputObject : type.getInputFields()) {
      String fieldName = fieldName(inputObject);
      FieldSpec.Builder field =
          FieldSpec.builder(inputObject.getType().formatInput(), fieldName, Modifier.PRIVATE);
      if (!fieldName.equals(inputObject.getName())) {
        // JSON-B serializes inputs by field name: keep the schema's.
        field.addAnnotation(
            AnnotationSpec.builder(JsonbProperty.class)
                .addMember("value", "$S", inputObject.getName())
                .build());
      }
      classBuilder.addField(field.build());

      MethodSpec getter = Helpers.getter(fieldName, inputObject.getType().formatInput());
      MethodSpec setter = Helpers.setter(fieldName, inputObject.getType().formatOutput());
      MethodSpec withSetter =
          Helpers.withSetter(
              inputObject,
              inputObject.getType().formatInput(),
              ClassName.bestGuess(Helpers.formatName(type)));
      methods.add(getter);
      methods.add(setter);
      methods.add(withSetter);
      legacyNames.add(
          Helpers.getter(inputObject.getName(), inputObject.getType().formatInput()).name());
      legacyNames.add(
          Helpers.setter(inputObject.getName(), inputObject.getType().formatOutput()).name());
      legacyNames.add(Helpers.legacyWithSetterName(inputObject));
    }
    Set<String> methodNames = methods.stream().map(MethodSpec::name).collect(Collectors.toSet());
    for (int i = 0; i < methods.size(); i++) {
      MethodSpec method = methods.get(i);
      classBuilder.addMethod(method);
      // Accessors renamed by identifier words keep their old names as deprecated aliases.
      String legacyName = legacyNames.get(i);
      if (!legacyName.equals(method.name()) && !methodNames.contains(legacyName)) {
        classBuilder.addMethod(Helpers.deprecatedAlias(method, legacyName));
      }
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
      // The map's keys are sent as the input's field names: keep the schema's.
      toMapMethod.beginControlFlow("if (this.$L != null)", fieldName(inputObject));
      toMapMethod.addStatement(
          "map.put($S, this.$L)", inputObject.getName(), fieldName(inputObject));
      toMapMethod.endControlFlow();
    }
    toMapMethod.addStatement("return map");
    classBuilder.addMethod(toMapMethod.build());

    return classBuilder.build();
  }

  /**
   * The Java field of an input field: from its identifier words when the schema JSON has them, else
   * the schema name as is.
   */
  private static String fieldName(InputObject inputObject) {
    if (inputObject.getWords() == null || inputObject.getWords().isEmpty()) {
      return inputObject.getName();
    }
    return Helpers.formatName(inputObject);
  }
}
