package io.dagger.codegen.introspection;

import com.palantir.javapoet.*;

import javax.lang.model.element.Modifier;
import java.nio.charset.Charset;
import java.nio.file.Path;

class ScalarVisitor extends AbstractVisitor {
  public ScalarVisitor(Schema schema, Path targetDirectory, Charset encoding) {
    super(schema, targetDirectory, encoding);
  }

  @Override
  TypeSpec generateType(Type type) {
    TypeSpec.Builder classBuilder =
        TypeSpec.classBuilder(Helpers.formatName(type))
            .addJavadoc(type.getDescription())
            .addModifiers(Modifier.PUBLIC)
            .superclass(
                ParameterizedTypeName.get(
                    ClassName.bestGuess("Scalar"), ClassName.get(String.class)));

    MethodSpec constructor =
        MethodSpec.constructorBuilder()
            .addParameter(ClassName.get(String.class), "value")
            .addStatement("super(value)")
            .build();
    classBuilder.addMethod(constructor);

    ClassName className = ClassName.bestGuess(Helpers.formatName(type));
    MethodSpec fromMethod =
        MethodSpec.methodBuilder("from")
            .addModifiers(Modifier.PUBLIC, Modifier.STATIC)
            .addParameter(ClassName.get(String.class), "value")
            .returns(className)
            .addStatement("return new $T(value)", className)
            .build();
    classBuilder.addMethod(fromMethod);

    return classBuilder.build();
  }
}
