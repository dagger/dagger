package io.dagger.codegen.introspection;

import com.palantir.javapoet.*;
import java.nio.charset.Charset;
import java.nio.file.Path;
import javax.lang.model.element.Modifier;

public class EnumVisitor extends AbstractVisitor {

  public EnumVisitor(Schema schema, Path targetDirectory, Charset encoding) {
    super(schema, targetDirectory, encoding);
  }

  @Override
  TypeSpec generateType(Type type) {
    TypeSpec.Builder classBuilder =
        TypeSpec.enumBuilder(Helpers.formatName(type))
            .addJavadoc(type.getDescription())
            .addModifiers(Modifier.PUBLIC);

    for (EnumValue enumValue : type.getEnumValues()) {
      TypeSpec.Builder enumTypeBuilder =
          TypeSpec.anonymousClassBuilder("").addJavadoc(enumValue.getDescription());
      if (enumValue.isDeprecated()) {
        enumTypeBuilder.addAnnotation(Deprecated.class);
      }
      classBuilder.addEnumConstant(enumValue.getName(), enumTypeBuilder.build());
    }

    return classBuilder.build();
  }
}
