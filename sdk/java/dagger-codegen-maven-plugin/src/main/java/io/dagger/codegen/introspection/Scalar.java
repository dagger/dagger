package io.dagger.codegen.introspection;

public enum Scalar {
  ScalarInt("Int"),
  ScalarFloat("Float"),
  ScalarString("String"),
  ScalarBoolean("Boolean");

  private String type;

  Scalar(String scalarType) {
    this.type = scalarType;
  }
}
