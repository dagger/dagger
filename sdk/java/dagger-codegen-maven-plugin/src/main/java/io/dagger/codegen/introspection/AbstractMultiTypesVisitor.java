package io.dagger.codegen.introspection;

import com.palantir.javapoet.TypeSpec;
import java.io.IOException;
import java.nio.charset.Charset;
import java.nio.file.Path;
import java.util.List;

abstract class AbstractMultiTypesVisitor extends CodeWriter {

  private Schema schema;

  public AbstractMultiTypesVisitor(Schema schema, Path targetDirectory, Charset encoding) {
    super(targetDirectory, encoding);
    this.schema = schema;
  }

  void visit(List<Type> types) throws IOException {
    TypeSpec typeSpec = generateType(types);
    write(typeSpec);
  }

  public Schema getSchema() {
    return schema;
  }

  abstract TypeSpec generateType(List<Type> types);
}
