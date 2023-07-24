package org.chelonix.dagger.codegen.introspection;

import com.squareup.javapoet.*;

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
        TypeSpec.Builder classBuilder = TypeSpec.classBuilder(Helpers.formatName(type))
                .addJavadoc(type.getDescription())
                .addModifiers(Modifier.PUBLIC)
                .addSuperinterface(ClassName.bestGuess("InputValue"));

        for (InputValue inputValue: type.getInputFields()) {

            classBuilder.addField(FieldSpec.builder(
                    inputValue.getType().formatInput(),
                    inputValue.getName(),
                    Modifier.PRIVATE).build());

            classBuilder.addMethod(Helpers.getter(inputValue.getName(), inputValue.getType().formatInput()));
            classBuilder.addMethod(Helpers.setter(inputValue.getName(), inputValue.getType().formatOutput()));
        }

        MethodSpec.Builder toMapMethod = MethodSpec.methodBuilder("toMap")
                .addModifiers(Modifier.PUBLIC)
                .addAnnotation(Override.class)
                .returns(ParameterizedTypeName.get(Map.class, String.class, Object.class))
                .addStatement("$1T map = new $1T()", ParameterizedTypeName.get(
                        HashMap.class, String.class, Object.class));
        for (InputValue inputValue: type.getInputFields()) {
            toMapMethod.addStatement("map.put(\"$1L\", this.$1L)", inputValue.getName());
        }
        toMapMethod.addStatement("return map");
        classBuilder.addMethod(toMapMethod.build());

        return classBuilder.build();
    }
}
