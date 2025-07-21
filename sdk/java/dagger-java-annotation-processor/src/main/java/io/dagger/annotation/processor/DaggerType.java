package io.dagger.annotation.processor;

import com.palantir.javapoet.ClassName;
import com.palantir.javapoet.CodeBlock;
import com.palantir.javapoet.ParameterizedTypeName;
import io.dagger.client.Dagger;
import io.dagger.client.TypeDefKind;
import io.dagger.module.info.TypeInfo;
import java.util.Set;
import javax.lang.model.type.TypeKind;

public abstract class DaggerType {
  protected static Set<String> knownEnums = Set.of();

  public static void setKnownEnums(Set<String> enums) {
    knownEnums = enums;
  }

  abstract CodeBlock toDaggerTypeDef();

  abstract CodeBlock toJavaType();

  abstract String toKind();

  abstract String toName();

  CodeBlock toClass() {
    return CodeBlock.of("$L.class", toJavaType());
  }

  boolean isList() {
    return false;
  }

  public static DaggerType of(TypeInfo ti) {
    String name = ti.typeName();
    String kindName = ti.kindName();

    if (knownEnums.contains(name)) {
      return new Enum(name, name.substring(name.lastIndexOf('.') + 1));
    }

    switch (name) {
      case "void" -> {
        return new Kind("void", true);
      }
      case "boolean" -> {
        return new Kind("boolean", false);
      }
      case "int", "long", "short", "byte" -> {
        return new Kind("int", false);
      }
      case "float", "double" -> {
        return new Kind("float", false);
      }
    }

    if (name.startsWith("java.util.List<")) {
      return new List(name.substring("java.util.List<".length(), name.length() - 1));
    }
    if (!kindName.isEmpty() && kindName.equals(TypeKind.ARRAY.name())) {
      // in that case the type name is com.example.Type[]
      // so we remove the [] to get the underlying type
      return new Array(name.substring(0, name.length() - 2));
    }

    if (name.startsWith("java.util.Optional<")) {
      return of(name.substring("java.util.Optional<".length(), name.length() - 1));
    }

    try {
      var clazz = Class.forName(name);
      if (clazz.isEnum()) {
        return new Enum(name, name.substring(name.lastIndexOf('.') + 1));
      } else if (io.dagger.client.Scalar.class.isAssignableFrom(clazz)) {
        return new Scalar(name, name.substring(name.lastIndexOf('.') + 1));
      }
    } catch (ClassNotFoundException e) {
      // we are ignoring here any ClassNotFoundException
      // not ideal, but we only want to know if it's an enum or a Scalar
    }

    try {
      if (name.startsWith("java.lang.")) {
        String simpleName = name.substring(name.lastIndexOf('.') + 1);
        // check if it exists as TypeDefKind
        TypeDefKind.valueOf("%s_KIND".formatted(simpleName.toUpperCase()));
        return new Kind(simpleName, false);
      }
    } catch (IllegalArgumentException e) {
      // it means valueOf failed
    }

    return new Object(name, name.substring(name.lastIndexOf('.') + 1));
  }

  public static DaggerType of(String name) {
    return switch (name) {
      case "boolean" -> of(new TypeInfo(name, TypeKind.BOOLEAN.name()));
      case "byte" -> of(new TypeInfo(name, TypeKind.BYTE.name()));
      case "short" -> of(new TypeInfo(name, TypeKind.SHORT.name()));
      case "int" -> of(new TypeInfo(name, TypeKind.INT.name()));
      case "long" -> of(new TypeInfo(name, TypeKind.LONG.name()));
      case "char" -> of(new TypeInfo(name, TypeKind.CHAR.name()));
      case "float" -> of(new TypeInfo(name, TypeKind.FLOAT.name()));
      case "double" -> of(new TypeInfo(name, TypeKind.DOUBLE.name()));
      case "void" -> of(new TypeInfo(name, TypeKind.VOID.name()));
      default -> {
        if (name.endsWith("[]")) {
          yield of(new TypeInfo(name, TypeKind.ARRAY.name()));
        } else {
          yield of(new TypeInfo(name, ""));
        }
      }
    };
  }

  public static class Enum extends DaggerType {
    private final String qualifiedName;
    private final String simpleName;

    public Enum(String qualifiedName, String simpleName) {
      this.qualifiedName = qualifiedName;
      this.simpleName = simpleName;
    }

    @Override
    CodeBlock toDaggerTypeDef() {
      return CodeBlock.of("$T.dag().typeDef().withEnum($S)", Dagger.class, simpleName);
    }

    @Override
    CodeBlock toJavaType() {
      return CodeBlock.of("$T", ClassName.bestGuess(qualifiedName));
    }

    @Override
    String toKind() {
      return "ENUM_KIND";
    }

    @Override
    public String toName() {
      return simpleName;
    }
  }

  public static class Kind extends DaggerType {
    private final String simpleName;
    private final boolean isOptional;

    public Kind(String simpleName, boolean isOptional) {
      this.simpleName = simpleName;
      this.isOptional = isOptional;
    }

    @Override
    CodeBlock toDaggerTypeDef() {
      String name =
          switch (simpleName) {
            case "byte", "short", "int", "long", "char" -> "integer";
            case "float", "double" -> "float";
            default -> simpleName;
          };
      CodeBlock.Builder cb =
          CodeBlock.builder()
              .add(
                  "$T.dag().typeDef().withKind($T.$L)",
                  Dagger.class,
                  TypeDefKind.class,
                  "%s_KIND".formatted(name.toUpperCase()));
      if (isOptional) {
        cb.add(".withOptional(true)");
      }
      return cb.build();
    }

    @Override
    CodeBlock toJavaType() {
      return switch (simpleName) {
        case "boolean" -> CodeBlock.of("$T", boolean.class);
        case "Boolean" -> CodeBlock.of("$T", Boolean.class);
        case "byte" -> CodeBlock.of("$T", byte.class);
        case "Byte" -> CodeBlock.of("$T", Byte.class);
        case "short" -> CodeBlock.of("$T", short.class);
        case "Short" -> CodeBlock.of("$T", Short.class);
        case "int" -> CodeBlock.of("$T", int.class);
        case "Integer" -> CodeBlock.of("$T", Integer.class);
        case "long" -> CodeBlock.of("$T", long.class);
        case "Long" -> CodeBlock.of("$T", Long.class);
        case "char" -> CodeBlock.of("$T", char.class);
        case "Character" -> CodeBlock.of("$T", Character.class);
        case "float" -> CodeBlock.of("$T", float.class);
        case "Float" -> CodeBlock.of("$T", Float.class);
        case "double" -> CodeBlock.of("$T", double.class);
        case "Double" -> CodeBlock.of("$T", Double.class);
        case "void" -> CodeBlock.of("$T", void.class);
        case "String" -> CodeBlock.of("$T", String.class);
        default -> throw new RuntimeException("not implemented for %s type".formatted(simpleName));
      };
    }

    @Override
    String toKind() {
      return switch (simpleName) {
        case "boolean" -> "BOOLEAN_KIND";
        case "Boolean" -> "BOOLEAN_KIND";
        case "byte" -> "INTEGER_KIND";
        case "Byte" -> "INTEGER_KIND";
        case "short" -> "INTEGER_KIND";
        case "Short" -> "INTEGER_KIND";
        case "int" -> "INTEGER_KIND";
        case "Integer" -> "INTEGER_KIND";
        case "long" -> "INTEGER_KIND";
        case "Long" -> "INTEGER_KIND";
        case "char" -> "INTEGER_KIND";
        case "Character" -> "INTEGER_KIND";
        case "float" -> "FLOAT_KIND";
        case "Float" -> "FLOAT_KIND";
        case "double" -> "FLOAT_KIND";
        case "Double" -> "FLOAT_KIND";
        case "void" -> "VOID_KIND";
        case "String" -> "STRING_KIND";
        default -> throw new RuntimeException("not implemented for %s type".formatted(simpleName));
      };
    }

    @Override
    public String toName() {
      return simpleName;
    }
  }

  public static class Scalar extends DaggerType {
    private final String simpleName;
    private final String qualifiedName;

    public Scalar(String qualifiedName, String simpleName) {
      this.qualifiedName = qualifiedName;
      this.simpleName = simpleName;
    }

    @Override
    CodeBlock toDaggerTypeDef() {
      return CodeBlock.of("$T.dag().typeDef().withScalar($S)", Dagger.class, simpleName);
    }

    @Override
    CodeBlock toJavaType() {
      return CodeBlock.of("$T", ClassName.bestGuess(qualifiedName));
    }

    @Override
    String toKind() {
      return "SCALAR_KIND";
    }

    @Override
    public String toName() {
      return simpleName;
    }
  }

  public static class Object extends DaggerType {
    private final String qualifiedName;
    private final String simpleName;

    public Object(String qualifiedName, String simpleName) {
      this.qualifiedName = qualifiedName;
      this.simpleName = simpleName;
    }

    @Override
    CodeBlock toDaggerTypeDef() {
      return CodeBlock.of("$T.dag().typeDef().withObject($S)", Dagger.class, simpleName);
    }

    @Override
    CodeBlock toJavaType() {
      return CodeBlock.of("$T", ClassName.bestGuess(qualifiedName));
    }

    @Override
    String toKind() {
      return "OBJECT_KIND";
    }

    @Override
    public String toName() {
      return simpleName;
    }
  }

  public static class List extends DaggerType {
    private final String innerName;

    public List(String innerName) {
      this.innerName = innerName;
    }

    @Override
    CodeBlock toDaggerTypeDef() {
      CodeBlock.Builder cb =
          CodeBlock.builder()
              .add("$T.dag().typeDef().withListOf(", Dagger.class)
              .add(of(innerName).toDaggerTypeDef())
              .add(")");
      return cb.build();
    }

    @Override
    CodeBlock toJavaType() {
      return CodeBlock.of(
          "$T",
          ParameterizedTypeName.get(
              ClassName.get("java.util", "List"), ClassName.bestGuess(innerName)));
    }

    @Override
    CodeBlock toClass() {
      return CodeBlock.of("$L[].class", of(innerName).toJavaType());
    }

    @Override
    boolean isList() {
      return true;
    }

    @Override
    String toKind() {
      return "LIST_KIND";
    }

    @Override
    public String toName() {
      return "list";
    }
  }

  public static class Array extends DaggerType {
    private final String innerName;

    public Array(String innerName) {
      this.innerName = innerName;
    }

    @Override
    CodeBlock toDaggerTypeDef() {
      CodeBlock.Builder cb =
          CodeBlock.builder()
              .add("$T.dag().typeDef().withListOf(", Dagger.class)
              .add(of(innerName).toDaggerTypeDef())
              .add(")");
      return cb.build();
    }

    @Override
    CodeBlock toJavaType() {
      return CodeBlock.of("$L[]", of(innerName).toJavaType());
    }

    @Override
    String toKind() {
      return "LIST_KIND";
    }

    @Override
    public String toName() {
      return "array";
    }
  }
}
