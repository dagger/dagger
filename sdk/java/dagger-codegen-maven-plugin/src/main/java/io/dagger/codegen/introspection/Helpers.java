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

  static ClassName convertScalarToObject(String typeName) {
    if (typeName.endsWith("ID")) {
      return ClassName.bestGuess(typeName.substring(0, typeName.length() - 2));
    }
    return ClassName.bestGuess(typeName);
  }

  /** returns true if the field returns an ID that should be converted into an object. */
  static boolean isIdToConvert(Field field) {
    return !"id".equals(field.getName())
        && field.getTypeRef().isScalar()
        && field
            .getParentObject()
            .getName()
            .equals(
                field
                    .getTypeRef()
                    .getTypeName()
                    .substring(0, field.getTypeRef().getTypeName().length() - 2));
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

  /** Fix using '$' char in javadoc */
  static String escapeJavadoc(String str) {
    if (str == null) {
      return "";
    }
    return str.replace("$", "$$").replace("&", "&amp;");
  }
}
