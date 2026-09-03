package io.dagger.codegen.introspection;

import static org.apache.commons.lang3.StringUtils.capitalize;

import com.palantir.javapoet.ClassName;
import com.palantir.javapoet.MethodSpec;
import com.palantir.javapoet.ParameterSpec;
import com.palantir.javapoet.TypeName;
import java.util.List;
import javax.lang.model.element.Modifier;

public class Helpers {

  private static final List<String> JAVA_KEYWORDS =
      List.of(
          "abstract",
          "assert",
          "continue",
          "for",
          "new",
          "switch",
          "assert",
          "default",
          "goto",
          "package",
          "synchronized",
          "boolean",
          "do",
          "if",
          "private",
          "this",
          "break",
          "double",
          "implements",
          "protected",
          "throw",
          "byte",
          "else",
          "import",
          "public",
          "throws",
          "case",
          "enum",
          "instanceof",
          "return",
          "transient",
          "catch",
          "extends",
          "int",
          "short",
          "try",
          "char",
          "final",
          "interface",
          "static",
          "void",
          "class",
          "finally",
          "long",
          "strictfp",
          "volatile",
          "const",
          "float",
          "native",
          "super",
          "while");

  static ClassName convertScalarToObject(String typeName, String expectedType) {
    if (expectedType != null && !expectedType.isEmpty()) {
      return ClassName.bestGuess(expectedType);
    }
    if (typeName.endsWith("ID") && typeName.length() > 2) {
      return ClassName.bestGuess(typeName.substring(0, typeName.length() - 2));
    }
    return ClassName.bestGuess(typeName);
  }

  static ClassName convertScalarToObject(String typeName) {
    return convertScalarToObject(typeName, null);
  }

  /**
   * Returns true if the field returns an ID handle to an object: a unified ID carrying an
   * expectedType directive (sync(), spawn(), ...) or a legacy FooID scalar. The generated method
   * loads that object from the returned ID rather than exposing the ID itself.
   */
  static boolean isIdToConvert(Field field) {
    return idHandleType(field) != null;
  }

  /**
   * Returns the name of the object type an ID-handle field resolves to, or null when the field does
   * not return an ID handle (see {@link #isIdToConvert}). The expectedType directive names it:
   * sync-likes return their parent, and LLM.spawn returns an Agent rather than its ID.
   */
  static String idHandleType(Field field) {
    if ("id".equals(field.getName())) {
      return null;
    }
    if (!field.getTypeRef().isScalar()) {
      return null;
    }
    String typeName = field.getTypeRef().getTypeName();
    // Unified ID: the expectedType directive names the object
    if ("ID".equals(typeName)) {
      String expectedType = field.getExpectedType();
      return expectedType == null || expectedType.isEmpty() ? null : expectedType;
    }
    // Legacy: FooID scalar
    if (typeName != null && typeName.endsWith("ID") && typeName.length() > 2) {
      return typeName.substring(0, typeName.length() - 2);
    }
    return null;
  }

  static List<Field> getArrayField(Field field, Schema schema) {
    TypeRef fieldType = field.getTypeRef();
    if (!fieldType.isOptional()) {
      fieldType = fieldType.getOfType();
    }
    if (!fieldType.isList()) {
      throw new IllegalArgumentException("field is not a list");
    }
    fieldType = fieldType.getOfType();
    if (!fieldType.isOptional()) {
      fieldType = fieldType.getOfType();
    }
    final String typeName = fieldType.getName();
    Type schemaType =
        schema.getTypes().stream()
            .filter(t -> typeName.equals(t.getName()))
            .findFirst()
            .orElseThrow(
                () ->
                    new IllegalArgumentException(
                        String.format("Schema type %s not found", typeName)));
    return schemaType.getFields().stream().filter(f -> f.getTypeRef().isScalar()).toList();
  }

  static String formatName(Type type) {
    if ("Query".equals(type.getName())) {
      return "Client";
    } else {
      return capitalize(type.getName());
    }
  }

  static String formatName(Field field) {
    if ("Container".equals(field.getParentObject().getName()) && "import".equals(field.getName())) {
      return "importTarball";
    } else if (JAVA_KEYWORDS.contains(field.getName())) {
      return field.getName() + "_";
    } else {
      return field.getName();
    }
  }

  static String formatName(InputObject arg) {
    if (JAVA_KEYWORDS.contains(arg.getName())) {
      return "_" + arg.getName();
    } else {
      return arg.getName();
    }
  }

  static MethodSpec getter(String var, TypeName type) {
    String prefix =
        (TypeName.BOOLEAN.equals(type) || ClassName.get(Boolean.class).equals(type)) ? "is" : "get";
    return MethodSpec.methodBuilder(prefix + capitalize(var))
        .addModifiers(Modifier.PUBLIC)
        .returns(type)
        .addStatement("return this.$L", var)
        .build();
  }

  static MethodSpec setter(String var, TypeName type) {
    return MethodSpec.methodBuilder("set" + capitalize(var))
        .addModifiers(Modifier.PUBLIC)
        .addParameter(ParameterSpec.builder(type, var).build())
        .addStatement("this.$1L = $1L", var)
        .build();
  }

  static MethodSpec withSetter(InputObject var, TypeName type, TypeName returnType) {
    return withSetter(var, type, returnType, null);
  }

  static MethodSpec withSetter(InputObject var, TypeName type, TypeName returnType, String doc) {
    MethodSpec.Builder builder =
        MethodSpec.methodBuilder("with" + capitalize(var.getName()))
            .addModifiers(Modifier.PUBLIC)
            .addParameter(type, Helpers.formatName(var))
            .returns(returnType)
            .addStatement("this.$1L = $1L", Helpers.formatName(var))
            .addStatement("return this");
    if (doc != null) {
      builder.addJavadoc(Helpers.escapeJavadoc(doc) + "\n");
    }
    return builder.build();
  }

  /**
   * Escape characters that have a special meaning in javadoc.
   *
   * <p>'$' is escaped for JavaPoet's format strings, while '&amp;', '&lt;' and '&gt;' are HTML
   * entities so that descriptions containing markup-like tokens (e.g. {@code <name>}) don't get
   * parsed as HTML tags by the javadoc tool. The comment terminator is escaped so glob examples
   * such as {@code **&#47;node_modules/**} cannot end the generated Javadoc early.
   */
  static String escapeJavadoc(String str) {
    if (str == null) {
      return "";
    }
    return str.replace("$", "$$")
        .replace("&", "&amp;")
        .replace("<", "&lt;")
        .replace(">", "&gt;")
        .replace("*/", "*&#47;");
  }
}
