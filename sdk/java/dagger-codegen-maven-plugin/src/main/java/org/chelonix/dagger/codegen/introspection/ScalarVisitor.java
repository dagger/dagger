package org.chelonix.dagger.codegen.introspection;

import com.squareup.javapoet.*;

import javax.lang.model.element.Modifier;
import java.nio.charset.Charset;
import java.nio.file.Path;

class ScalarVisitor extends AbstractVisitor {
    public ScalarVisitor(Schema schema, Path targetDirectory, Charset encoding) {
        super(schema, targetDirectory, encoding);
    }

    @Override
    TypeSpec generateType(Type type) {
        TypeSpec.Builder classBuilder = TypeSpec.classBuilder(Helpers.formatName(type))
                .addJavadoc(type.getDescription())
                .addModifiers(Modifier.PUBLIC)
                .superclass(ParameterizedTypeName.get(
                        ClassName.bestGuess("Scalar"),
                        ClassName.get(String.class)));

        MethodSpec constructor = MethodSpec.constructorBuilder()
                .addParameter(ClassName.get(String.class), "value")
                .addStatement("super(value)").build();

        classBuilder.addMethod(constructor);

        return classBuilder.build();
    }
}
