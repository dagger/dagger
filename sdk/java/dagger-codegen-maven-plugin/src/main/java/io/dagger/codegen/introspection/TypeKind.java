package io.dagger.codegen.introspection;

public enum TypeKind {
  SCALAR("SCALAR"),
  OBJECT("OBJECT"),
  INTERFACE("INTERFACE"),
  UNION("UNION"),
  ENUM("ENUM"),
  INPUT_OBJECT("INPUT_OBJECT"),
  LIST("LIST"),
  NON_NULL("NON_NULL");

  private final String kind;

  TypeKind(String kind) {
    this.kind = kind;
  }
}
