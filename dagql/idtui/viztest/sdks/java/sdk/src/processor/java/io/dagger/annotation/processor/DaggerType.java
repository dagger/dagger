package io.dagger.annotation.processor;

import com.palantir.javapoet.ClassName;
import com.palantir.javapoet.CodeBlock;
import com.palantir.javapoet.ParameterizedTypeName;
import io.dagger.client.GraphQLType;
import io.dagger.client.modules.core.Core;
import io.dagger.client.modules.core.TypeDefKind;
import io.dagger.module.info.TypeInfo;
import java.util.Set;
import javax.lang.model.type.TypeKind;

public abstract class DaggerType {
  protected static Set<String> knownEnums = Set.of();

  private static final String CORE_PACKAGE = "io.dagger.client.modules.core";

  /**
   * The SDK's ID and JSON scalar classes. Their names depend on the schema the SDK was generated
   * from: Java's convention ({@code Id}, {@code Json}) with engine-formatted names, the schema's
   * ({@code ID}, {@code JSON}) without.
   */
  static final ClassName ID_CLASS = coreClass("Id", "ID");

  static final ClassName JSON_CLASS = coreClass("Json", "JSON");

  private static ClassName coreClass(String conventionName, String schemaName) {
    try {
      Class<?> clazz = Class.forName(CORE_PACKAGE + "." + conventionName);
      GraphQLType graphQLType = clazz.getAnnotation(GraphQLType.class);
      if (graphQLType != null && graphQLType.value().equals(schemaName)) {
        return ClassName.get(CORE_PACKAGE, conventionName);
      }
    } catch (ClassNotFoundException | LinkageError e) {
      // generated without formatted names
    }
    return ClassName.get(CORE_PACKAGE, schemaName);
  }

  /**
   * The GraphQL type a class stands for: the type its {@link GraphQLType} names, for SDK classes
   * named by Java's convention ({@code JsonValue} for {@code JSONValue}), else its simple name.
   */
  static String graphQLName(String qualifiedName, String simpleName) {
    try {
      GraphQLType graphQLType = Class.forName(qualifiedName).getAnnotation(GraphQLType.class);
      if (graphQLType != null) {
        return graphQLType.value();
      }
    } catch (ClassNotFoundException | LinkageError e) {
      // not an SDK class: the module's own types are named by their simple names
    }
    return simpleName;
  }

  public static void setKnownEnums(Set<String> enums) {
    knownEnums = enums;
  }

  abstract CodeBlock toDaggerTypeDef();

  /** The type definition of {@link #toDaggerTypeDef()}, as Dang source for the entrypoint. */
  abstract String toDangTypeDef();

  abstract CodeBlock toJavaType();

  CodeBlock toClass() {
    return CodeBlock.of("$L.class", toJavaType());
  }

  boolean isList() {
    return false;
  }

  CodeBlock valueForSerialization(String name) {
    return CodeBlock.of("$L", name);
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
      return new Optional(of(name.substring("java.util.Optional<".length(), name.length() - 1)));
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
      return CodeBlock.of(
          "$T.core().typeDef().withEnum($S)", Core.class, graphQLName(qualifiedName, simpleName));
    }

    @Override
    String toDangTypeDef() {
      return "typeDef.withEnum(" + Dang.quote(graphQLName(qualifiedName, simpleName)) + ")";
    }

    @Override
    CodeBlock toJavaType() {
      return CodeBlock.of("$T", ClassName.bestGuess(qualifiedName));
    }
  }

  public static class Kind extends DaggerType {
    private final String simpleName;
    private final boolean isOptional;

    public Kind(String simpleName, boolean isOptional) {
      this.simpleName = simpleName;
      this.isOptional = isOptional;
    }

    private String kindName() {
      String name =
          switch (simpleName) {
            case "byte", "short", "int", "long", "char" -> "integer";
            case "float", "double" -> "float";
            default -> simpleName;
          };
      return "%s_KIND".formatted(name.toUpperCase());
    }

    @Override
    CodeBlock toDaggerTypeDef() {
      CodeBlock.Builder cb =
          CodeBlock.builder()
              .add(
                  "$T.core().typeDef().withKind($T.$L)", Core.class, TypeDefKind.class, kindName());
      if (isOptional) {
        cb.add(".withOptional(true)");
      }
      return cb.build();
    }

    @Override
    String toDangTypeDef() {
      String def = "typeDef.withKind(TypeDefKind." + kindName() + ")";
      return isOptional ? def + ".withOptional(true)" : def;
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
      return CodeBlock.of(
          "$T.core().typeDef().withScalar($S)", Core.class, graphQLName(qualifiedName, simpleName));
    }

    @Override
    String toDangTypeDef() {
      return "typeDef.withScalar(" + Dang.quote(graphQLName(qualifiedName, simpleName)) + ")";
    }

    @Override
    CodeBlock toJavaType() {
      return CodeBlock.of("$T", ClassName.bestGuess(qualifiedName));
    }
  }

  public static class Optional extends DaggerType {
    private final DaggerType inner;

    public Optional(DaggerType inner) {
      this.inner = inner;
    }

    @Override
    CodeBlock toDaggerTypeDef() {
      return CodeBlock.builder().add(inner.toDaggerTypeDef()).add(".withOptional(true)").build();
    }

    @Override
    String toDangTypeDef() {
      return inner.toDangTypeDef() + ".withOptional(true)";
    }

    @Override
    CodeBlock toJavaType() {
      return CodeBlock.of("$T<$L>", java.util.Optional.class, inner.toJavaType());
    }

    @Override
    CodeBlock valueForSerialization(String name) {
      return CodeBlock.of("$L.orElse(null)", name);
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
      return CodeBlock.of(
          "$T.core().typeDef().withObject($S)", Core.class, graphQLName(qualifiedName, simpleName));
    }

    @Override
    String toDangTypeDef() {
      return "typeDef.withObject(" + Dang.quote(graphQLName(qualifiedName, simpleName)) + ")";
    }

    @Override
    CodeBlock toJavaType() {
      return CodeBlock.of("$T", ClassName.bestGuess(qualifiedName));
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
              .add("$T.core().typeDef().withListOf(", Core.class)
              .add(of(innerName).toDaggerTypeDef())
              .add(")");
      return cb.build();
    }

    @Override
    String toDangTypeDef() {
      return "typeDef.withListOf(" + of(innerName).toDangTypeDef() + ")";
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
              .add("$T.core().typeDef().withListOf(", Core.class)
              .add(of(innerName).toDaggerTypeDef())
              .add(")");
      return cb.build();
    }

    @Override
    String toDangTypeDef() {
      return "typeDef.withListOf(" + of(innerName).toDangTypeDef() + ")";
    }

    @Override
    CodeBlock toJavaType() {
      return CodeBlock.of("$L[]", of(innerName).toJavaType());
    }
  }
}
