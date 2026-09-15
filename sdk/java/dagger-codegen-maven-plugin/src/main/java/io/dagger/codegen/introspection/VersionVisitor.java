package io.dagger.codegen.introspection;

import com.palantir.javapoet.FieldSpec;
import com.palantir.javapoet.TypeSpec;
import java.io.IOException;
import java.nio.charset.Charset;
import java.nio.file.Path;
import javax.lang.model.element.Modifier;

public class VersionVisitor extends CodeWriter {

  public VersionVisitor(Path targetDirectory, Charset encoding) {
    super(targetDirectory, encoding);
  }

  public void visit(String version) throws IOException {
    TypeSpec typeSpec = generateInterface(version);
    write(typeSpec);
  }

  private TypeSpec generateInterface(String version) {
    TypeSpec.Builder interfaceBuilder =
        TypeSpec.interfaceBuilder("Version")
            .addJavadoc("Dagger engine version")
            .addModifiers(Modifier.PUBLIC);

    FieldSpec versionField =
        FieldSpec.builder(String.class, "VERSION")
            .addModifiers(Modifier.PUBLIC, Modifier.STATIC, Modifier.FINAL)
            .initializer("$S", version)
            .build();
    interfaceBuilder.addField(versionField);

    return interfaceBuilder.build();
  }
}
