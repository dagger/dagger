package org.chelonix.dagger.codegen.introspection;

import com.samskivert.mustache.Mustache;
import com.samskivert.mustache.Template;

import java.io.IOException;
import java.io.InputStreamReader;
import java.io.Reader;
import java.io.Writer;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.function.Function;

import static org.apache.commons.lang3.StringUtils.capitalize;

public class CodegenVisitor implements SchemaVisitor {

    private Function<String, Writer> writerProvider;

    public CodegenVisitor(Function<String, Writer> writerProvider) {
        this.writerProvider = writerProvider;
    }

    @Override
    public void visitScalar(Type type) {
        try (Reader reader = new InputStreamReader(getClass().getClassLoader().getResourceAsStream("templates/scalar.mustache"));
             Writer writer = writerProvider.apply(String.format("org/chelonix/dagger/sdk/client/%s.java", type.getName())))
        {
            Template tmpl = Mustache.compiler().escapeHTML(false).compile(reader);
            Map<String, String> data = new HashMap<>(){{
                put("packageName", "org.chelonix.dagger.sdk.client");
                put("scalarType", "String");
                put("className", type.getName());
                put("classDescription", type.getDescription());
            }};
            writer.write(tmpl.execute(data));
        } catch (IOException ioe) {
            throw new RuntimeException(ioe);
        }
    }

    static class TypeScalarFieldContext {
        public String fieldName;
        public String fieldType;
        public String setter;

        public TypeScalarFieldContext(Field field) {
            this.fieldName = field.getName();
            this.fieldType = formatInputType(field.getTypeRef(), "");
            this.setter = "set" + capitalize(field.getName());
        }
    }

    static class TypeFieldContext {
        public String fieldName;
        public String fieldDescription;
        public String returnType;
        public List<FieldArgContext> args;
        public boolean isScalar;
        public boolean continueChaining;

        public TypeFieldContext(Field field) {
            this.fieldName = field.getName();
            this.fieldDescription = field.getDescription().replace("\n", "<br/>");;
            this.returnType = formatInputType(field.getTypeRef(), "");
            this.args = field.getArgs().stream().map(FieldArgContext::new).toList();
            this.isScalar = field.getTypeRef().isScalar();
            this.continueChaining = !field.getTypeRef().isScalar() && !field.getTypeRef().isList();
        }
    }

    static class FieldArgContext {
        public String argType;
        public String argName;
        public String argDescription;

        public FieldArgContext(InputValue arg) {
            this.argType = formatInputType(arg.getType(), "");
            this.argName = arg.getName();
            this.argDescription = arg.getDescription().replace("\n", "<br/>");
        }
    }

    @Override
    public void visitObject(Type type) {
        try (Reader reader = new InputStreamReader(getClass().getClassLoader().getResourceAsStream("templates/object.mustache"));
             Writer writer = writerProvider.apply(String.format("org/chelonix/dagger/sdk/client/%s.java", type.getName())))
        {
            Template tmpl = Mustache.compiler().escapeHTML(false).compile(reader);
            Map<String, Object> data = new HashMap<>(){{
                put("packageName", "org.chelonix.dagger.sdk.client");
                put("className", type.getName());
                put("classDescription", type.getDescription());
                put("scalarFields", type.getFields().stream().filter(f -> f.getTypeRef().isScalar()).map(TypeScalarFieldContext::new).toList());
                put("fields", type.getFields().stream().map(TypeFieldContext::new).toList());
            }};
            writer.write(tmpl.execute(data));
        } catch (IOException ioe) {
            throw new RuntimeException(ioe);
        }
    }

    private static String formatInputType(TypeRef typeRef, String representation) {
        if (typeRef == null) {
            return "void";
        }
        switch (typeRef.getKind()) {
            case SCALAR -> {
                switch (typeRef.getName()) {
                    case "String" -> {
                        return "String";
                    }
                    case "Boolean" -> {
                        return "Boolean";
                    }
                    case "Int" -> {
                        return "Integer";
                    }
                    default -> {
                        return typeRef.getName();
                    }
                }
            }
            case OBJECT, ENUM, INPUT_OBJECT -> {
                return typeRef.getName();
            }
            case LIST -> {
                return String.format("List<%s>", formatInputType(typeRef.getOfType(), representation));
            }
            default -> {
                return formatInputType(typeRef.getOfType(), representation);
            }
        }
    }

    static final class InputValueContext {
        public String name;
        public String getter;
        public String setter;
        public String description;
        public String type;

        public InputValueContext(String name, String description, String type) {
            this.name = name;
            this.getter = ("Boolean".equals(type) ? "is" : "get") + capitalize(name);
            this.setter = "set" + capitalize(name);
            this.description = description;
            this.type = type;
        }
    }

    @Override
    public void visitInput(Type type) {
        try (Reader reader = new InputStreamReader(
                getClass().getClassLoader().getResourceAsStream("templates/input.mustache")))
        {
            Template tmpl = Mustache.compiler().escapeHTML(false).compile(reader);
            Map<String, Object> data = new HashMap<>(){{
                put("packageName", "org.chelonix.dagger.sdk.client");
                put("className", type.getName());
                put("classDescription", type.getDescription());
                put("fields", type.getInputFields().stream()
                        .map(v -> new InputValueContext(v.getName(), v.getDescription(), formatInputType(v.getType(), "")))
                        .toList());
            }};
            System.out.println(tmpl.execute(data));
        } catch (IOException ioe) {
            throw new RuntimeException(ioe);
        }
    }

    @Override
    public void visitEnum(Type type) {
        try (Reader reader = new InputStreamReader(
                getClass().getClassLoader().getResourceAsStream("templates/enum.mustache")))
        {
            Template tmpl = Mustache.compiler().escapeHTML(false).compile(reader);
            Map<String, Object> data = new HashMap<>(){{
                put("packageName", "org.chelonix.dagger.sdk.client");
                put("className", type.getName());
                put("classDescription", type.getDescription());
                put("fields", type.getEnumValues().stream().map(v -> v.getName().toUpperCase()).sorted().toList());
            }};
            System.out.println(tmpl.execute(data));
        } catch (IOException ioe) {
            throw new RuntimeException(ioe);
        }
    }
}
