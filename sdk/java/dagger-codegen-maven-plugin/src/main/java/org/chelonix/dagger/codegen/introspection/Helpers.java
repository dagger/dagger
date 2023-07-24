package org.chelonix.dagger.codegen.introspection;

import com.squareup.javapoet.ClassName;
import com.squareup.javapoet.MethodSpec;
import com.squareup.javapoet.ParameterSpec;
import com.squareup.javapoet.TypeName;

import javax.lang.model.element.Modifier;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

import static org.apache.commons.lang3.StringUtils.capitalize;

public class Helpers {

    private static final Map<String, String> CUSTOM_SCALARS = new HashMap<>() {{
        put("ContainerID", "Container");
        put("FileID", "File");
        put("DirectoryID", "Directory");
        put("SecretID", "Secret");
        put("SocketID","Socket");
        put("CacheID","CacheVolume");
        put("ProjectID","Project");
        put("ProjectCommandID", "ProjectCommand");
    }};

    static boolean isScalar(String typeName) {
        return CUSTOM_SCALARS.containsKey(typeName) || "Platform".equals(typeName);
    }

    static ClassName convertScalarToObject(String typeName) {
        if ("Platform".equals(typeName)) {
            return ClassName.bestGuess(typeName);
        }
        if (CUSTOM_SCALARS.containsKey(typeName)) {
                return ClassName.bestGuess(CUSTOM_SCALARS.get(typeName));
        }
        throw new IllegalArgumentException(String.format("Unsupported Scalar type: %s", typeName));
    }

    /**
     * returns true if the field returns an ID that should be converted into an object.
     */
    static boolean isIdToConvert(Field field) {
        return !"id".equals(field.getName()) &&
                field.getTypeRef().isScalar() &&
                field.getParentObject().getName().equals(CUSTOM_SCALARS.get(field.getTypeRef().getTypeName()));
    }

    static List<Field> getArrayField(Field field, Schema schema) {
        TypeRef fieldType = field.getTypeRef();
        if (! fieldType.isOptional()) {
            fieldType = fieldType.getOfType();
        }
        if (! fieldType.isList()) {
            throw new IllegalArgumentException("field is not a list");
        }
        fieldType = fieldType.getOfType();
        if (! fieldType.isOptional()) {
            fieldType = fieldType.getOfType();
        }
        final String typeName = fieldType.getName();
        Type schemaType = schema.getTypes().stream()
                .filter(t -> typeName.equals(t.getName()))
                .findFirst()
                .orElseThrow(() -> new IllegalArgumentException(
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

    static MethodSpec getter(String var, TypeName type) {
        String prefix = (TypeName.BOOLEAN.equals(type) || ClassName.get(Boolean.class).equals(type)) ? "is" : "get";
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

    /**
     * Fix using '$' char in javadoc
      */
    static String escapeJavadoc(String str) {
        return str.replace("$", "$$");
    }
}
