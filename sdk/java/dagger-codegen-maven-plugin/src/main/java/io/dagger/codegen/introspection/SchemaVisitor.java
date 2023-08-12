package io.dagger.codegen.introspection;

public interface SchemaVisitor {

  void visitScalar(Type type);

  void visitObject(Type type);

  void visitInput(Type type);

  void visitEnum(Type type);

  void visitVersion(String version);
}
