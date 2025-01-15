package io.dagger.codegen.introspection;

import com.palantir.javapoet.ClassName;
import com.palantir.javapoet.ParameterizedTypeName;
import com.palantir.javapoet.TypeName;

import java.util.List;

public class TypeRef {

  private TypeKind kind;
  private String name;
  private TypeRef ofType;

  public TypeKind getKind() {
    return kind;
  }

  public void setKind(TypeKind kind) {
    this.kind = kind;
  }

  public String getName() {
    return name;
  }

  public void setName(String name) {
    this.name = name;
  }

  public TypeRef getOfType() {
    return ofType;
  }

  public void setOfType(TypeRef ofType) {
    this.ofType = ofType;
  }

  public boolean isOptional() {
    return kind != TypeKind.NON_NULL;
  }

  public boolean isScalar() {
    TypeRef ref = this;
    if (ref.kind == TypeKind.NON_NULL) {
      ref = ref.ofType;
    }
    return ref.kind == TypeKind.SCALAR || ref.kind == TypeKind.ENUM;
  }

  public boolean isObject() {
    TypeRef ref = this;
    if (ref.kind == TypeKind.NON_NULL) {
      ref = ref.ofType;
    }
    return ref.kind == TypeKind.OBJECT;
  }

  public boolean isList() {
    TypeRef ref = this;
    if (ref.kind == TypeKind.NON_NULL) {
      ref = ref.ofType;
    }
    return ref.kind == TypeKind.LIST;
  }

  public boolean isListOfObject() {
    TypeRef ref = this;
    if (ref.kind == TypeKind.NON_NULL) {
      ref = ref.ofType;
    }
    if (ref.kind != TypeKind.LIST) {
      return false;
    }
    ref = ref.getOfType();
    if (ref.kind == TypeKind.NON_NULL) {
      ref = ref.ofType;
    }
    return ref.isObject();
  }

  public TypeRef getListElementType() {
    if (!isList()) {
      throw new IllegalArgumentException("Type is not a list");
    }
    TypeRef ref = this;
    while (ref.kind == TypeKind.NON_NULL || ref.kind == TypeKind.LIST) {
      ref = ref.ofType;
    }
    return ref;
  }

  public TypeName formatOutput() {
    return formatType(false);
  }

  public TypeName formatInput() {
    return formatType(true);
  }

  private TypeName formatType(boolean isInput) {
    // if (typeRef == null) {
    //    return "void";
    // }
    if ("Query".equals(getName())) {
      return ClassName.bestGuess("Client");
    }
    switch (getKind()) {
      case SCALAR -> {
        switch (getName()) {
          case "String" -> {
            return ClassName.get(String.class);
          }
          case "Boolean" -> {
            return ClassName.get(Boolean.class);
          }
          case "Int" -> {
            return ClassName.get(Integer.class);
          }
          default -> {
            if (!isInput) {
              return ClassName.bestGuess(getName());
            }
            return Helpers.convertScalarToObject(getName());
            //                        if (getName().endsWith("ID") && isInput) {
            //                            return getName().substring(0, getName().length() - 2);
            //                        }
            //                        return getName();
          }
        }
      }
      case OBJECT, ENUM, INPUT_OBJECT -> {
        return ClassName.bestGuess(getName());
      }
      case LIST -> {
        return ParameterizedTypeName.get(
            ClassName.get(List.class), getOfType().formatType(isInput));
        // return String.format("List<%s>", getOfType().formatType(isInput));
      }
      default -> {
        return getOfType().formatType(isInput);
      }
    }
  }

  public String getTypeName() {
    TypeRef ref = this;
    if (ref.kind == TypeKind.NON_NULL) {
      ref = ofType;
    }
    return ref.getName();
  }
}
