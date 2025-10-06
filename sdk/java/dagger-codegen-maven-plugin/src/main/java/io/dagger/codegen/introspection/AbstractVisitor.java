package io.dagger.codegen.introspection;

import com.palantir.javapoet.TypeSpec;
import java.io.IOException;
import java.nio.charset.Charset;
import java.nio.file.Path;

abstract class AbstractVisitor extends CodeWriter {

  private Schema schema;

  public AbstractVisitor(Schema schema, Path targetDirectory, Charset encoding) {
    super(targetDirectory, encoding);
    this.schema = schema;
  }

  void visit(Type type) throws IOException {
    TypeSpec typeSpec = generateType(type);
    write(typeSpec);
  }

  public Schema getSchema() {
    return schema;
  }

  abstract TypeSpec generateType(Type type);
}
