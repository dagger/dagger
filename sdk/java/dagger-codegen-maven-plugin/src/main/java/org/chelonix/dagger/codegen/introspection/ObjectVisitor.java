package org.chelonix.dagger.codegen.introspection;

import com.squareup.javapoet.*;

import javax.lang.model.element.Modifier;
import java.nio.charset.Charset;
import java.nio.file.Path;
import java.util.List;
import java.util.concurrent.ExecutionException;
import java.util.function.UnaryOperator;

import static org.apache.commons.lang3.StringUtils.capitalize;
import static org.chelonix.dagger.codegen.introspection.Helpers.*;
import static org.chelonix.dagger.codegen.introspection.Helpers.isIdToConvert;

class ObjectVisitor extends AbstractVisitor {
    public ObjectVisitor(Schema schema, Path targetDirectory, Charset encoding) {
        super(schema, targetDirectory, encoding);
    }

    private static MethodSpec withMethod(String var, TypeName type, TypeName returnType, String doc) {
        return MethodSpec.methodBuilder("with" + capitalize(var))
                .addModifiers(Modifier.PUBLIC)
                .addParameter(type, var)
                .returns(returnType)
                .addStatement("this.$1L = $1L", var)
                .addStatement("return this")
                .addJavadoc(escapeJavadoc(doc))
                .build();
    }

    @Override
    TypeSpec generateType(Type type) {
        TypeSpec.Builder classBuilder = TypeSpec.classBuilder(Helpers.formatName(type))
                .addJavadoc(type.getDescription())
                .addModifiers(Modifier.PUBLIC)
                //.addSuperinterface(ClassName.bestGuess("IdProvider"))
                .addField(FieldSpec.builder(ClassName.bestGuess("QueryBuilder"), "queryBuilder",Modifier.PRIVATE).build());

        if ("Query".equals(type.getName())) {
            MethodSpec constructor = MethodSpec.constructorBuilder()
                    .addParameter(ClassName.bestGuess("org.chelonix.dagger.client.engineconn.Connection"), "connection")
                    .addStatement("this.connection = connection")
                    .addStatement("this.queryBuilder = new QueryBuilder(connection.getGraphQLClient())")
                    .build();
            classBuilder.addMethod(constructor);
            classBuilder.addField(FieldSpec.builder(
                    ClassName.bestGuess("org.chelonix.dagger.client.engineconn.Connection"),
                    "connection",Modifier.PRIVATE).build());
            // AutoCloseable implementation
            classBuilder.addSuperinterface(AutoCloseable.class);
            MethodSpec closeMethod = MethodSpec.methodBuilder("close")
                    .addException(Exception.class)
                    .addModifiers(Modifier.PUBLIC)
                    .addStatement("this.connection.close()")
                    .build();
            classBuilder.addMethod(closeMethod);
        } else {
            // Object constructor for JSON deserialization
            MethodSpec constructor = MethodSpec.constructorBuilder()
                    .addModifiers(Modifier.PROTECTED)
                    .addJavadoc("Empty constructor for JSON-B deserialization")
                    .build();
            classBuilder.addMethod(constructor);

            for (Field scalarField : type.getFields().stream().filter(f -> f.getTypeRef().isScalar()).toList()) {
                // If Object has an "id" field, implement IdProvider interface
                if ("id".equals(scalarField.getName())) {
                    classBuilder.addSuperinterface(ParameterizedTypeName.get(
                            ClassName.bestGuess("IdProvider"),
                            scalarField.getTypeRef().formatOutput()));
                }
                classBuilder.addField(scalarField.getTypeRef().formatOutput(), scalarField.getName(), Modifier.PRIVATE);
            }
        }

        // Object constructor for query building
        MethodSpec constructor = MethodSpec.constructorBuilder()
                .addParameter(ClassName.bestGuess("QueryBuilder"), "queryBuilder")
                .addCode("this.queryBuilder = queryBuilder;")
                .build();
        classBuilder.addMethod(constructor);

        for (Field field: type.getFields())
        {
            if (field.hasOptionalArgs()) {
                buildFieldArgumentsHelpers(classBuilder, field, type);
                buildFieldMethod(classBuilder, field, true);
            }

            buildFieldMethod(classBuilder, field, false);
        }

        if (List.of("Container", "Directoy").contains(type.getName())) {
            ClassName thisType = ClassName.bestGuess(Helpers.formatName(type));
            String argName = type.getName().toLowerCase() + "Func";
            classBuilder.addMethod(MethodSpec.methodBuilder("with")
                    .addModifiers(Modifier.PUBLIC)
                    .addParameter(ParameterizedTypeName.get(ClassName.get(UnaryOperator.class), thisType), argName)
                    .returns(thisType)
                    .addStatement("return $L.apply(this)", argName)
                    .build());
        }
        return classBuilder.build();
    }

    private void buildFieldMethod(TypeSpec.Builder classBuilder, Field field, boolean withOptionalArgs) {
        MethodSpec.Builder fieldMethodBuilder = MethodSpec.methodBuilder(formatName(field)).addModifiers(Modifier.PUBLIC);
        TypeName returnType = "id".equals(field.getName()) ? field.getTypeRef().formatOutput() : field.getTypeRef().formatInput();
        fieldMethodBuilder.returns(returnType);
        List<ParameterSpec> mandatoryParams = field.getRequiredArgs().stream()
                .map(arg -> ParameterSpec.builder(
                        "Query".equals(field.getParentObject().getName()) && "id".equals(arg.getName()) ?
                                arg.getType().formatOutput() :
                                arg.getType().formatInput(),
                        arg.getName())
                        .addJavadoc(arg.getDescription())
                        .build())
                .toList();
        fieldMethodBuilder.addParameters(mandatoryParams);
        if (withOptionalArgs && field.hasOptionalArgs()) {
            fieldMethodBuilder.addParameter(ParameterSpec.builder(ClassName.bestGuess(capitalize(formatName(field)) + "Arguments"), "optArgs")
                    .addJavadoc("$L optional arguments", formatName(field))
                    .build()
            );
        }
        fieldMethodBuilder.addJavadoc(escapeJavadoc(field.getDescription()));
        //field.getRequiredArgs().forEach(arg -> fieldMethodBuilder.addJavadoc("\n@param $L $L", arg.getName(), arg.getDescription()));

        if (field.getTypeRef().isScalar() && !isIdToConvert(field) && !"Query".equals(field.getParentObject().getName())) {
            fieldMethodBuilder.beginControlFlow("if (this.$L != null)", formatName(field));
            fieldMethodBuilder.addStatement("return $L", formatName(field));
            fieldMethodBuilder.endControlFlow();
        }
        if (field.hasArgs()) {
            fieldMethodBuilder.addStatement("Arguments.Builder builder = Arguments.newBuilder()");
        }
        field.getRequiredArgs().forEach(arg -> fieldMethodBuilder.addStatement("builder.add($1S, $1L)", arg.getName()));
        if (field.hasArgs()) {
            fieldMethodBuilder.addStatement("Arguments fieldArgs = builder.build()");
        }
        if (withOptionalArgs && field.hasOptionalArgs()) {
            fieldMethodBuilder.addStatement("fieldArgs = fieldArgs.merge(optArgs.toArguments())");
        }
        if (field.hasArgs()) {
            fieldMethodBuilder.addStatement("QueryBuilder nextQueryBuilder = this.queryBuilder.chain($S, fieldArgs)", field.getName());
        } else {
            fieldMethodBuilder.addStatement("QueryBuilder nextQueryBuilder = this.queryBuilder.chain($S)", field.getName());
        }

        if (field.getTypeRef().isListOfObject()) {
            List<Field> arrayFields = getArrayField(field, getSchema());
            CodeBlock block = arrayFields.stream().map(f -> CodeBlock.of("$S", f.getName())).collect(CodeBlock.joining(",", "List.of(", ")"));
            fieldMethodBuilder.addStatement("nextQueryBuilder = nextQueryBuilder.chain($L)", block);
            fieldMethodBuilder.addStatement("return nextQueryBuilder.executeListQuery($L.class)",
                    field.getTypeRef().getListElementType().getName());
            fieldMethodBuilder
                    .addException(InterruptedException.class)
                    .addException(ExecutionException.class)
                    .addException(ClassName.bestGuess("DaggerQueryException"));
        } else if (field.getTypeRef().isList()) {
            fieldMethodBuilder.addStatement("return nextQueryBuilder.executeListQuery($L.class)",
                    field.getTypeRef().getListElementType().getName());
            fieldMethodBuilder
                    .addException(InterruptedException.class)
                    .addException(ExecutionException.class)
                    .addException(ClassName.bestGuess("DaggerQueryException"));
        } else if (isIdToConvert(field)) {
            fieldMethodBuilder.addStatement("nextQueryBuilder.executeQuery()");
            fieldMethodBuilder.addStatement("return this");
            fieldMethodBuilder
                    .addException(InterruptedException.class)
                    .addException(ExecutionException.class)
                    .addException(ClassName.bestGuess("DaggerQueryException"));
        } else if (field.getTypeRef().isObject()) {
            fieldMethodBuilder.addStatement("return new $L(nextQueryBuilder)", returnType);
        } else {
            fieldMethodBuilder.addStatement("return nextQueryBuilder.executeQuery($L.class)", returnType);
            fieldMethodBuilder
                    .addException(InterruptedException.class)
                    .addException(ExecutionException.class)
                    .addException(ClassName.bestGuess("DaggerQueryException"));
        }

        if (field.isDeprecated()) {
            fieldMethodBuilder.addAnnotation(Deprecated.class);
            fieldMethodBuilder.addJavadoc("\n@deprecated $L", field.getDeprecationReason());
        }

        classBuilder.addMethod(fieldMethodBuilder.build());
    }

    /**
     * Builds the class containing the optional arguments.
     * @param classBuilder
     * @param field
     * @param type
     */
    private void buildFieldArgumentsHelpers(TypeSpec.Builder classBuilder, Field field, Type type) {
        String fieldArgumentsClassName = capitalize(formatName(field))+"Arguments";

        /* Inner class XXXArguments */
        TypeSpec.Builder fieldArgumentsClassBuilder = TypeSpec
                .classBuilder(fieldArgumentsClassName)
                .addModifiers(Modifier.PUBLIC, Modifier.STATIC);
        List<FieldSpec> optionalArgFields = field.getOptionalArgs().stream()
                .map(arg -> FieldSpec.builder(
                        "id".equals(arg.getName()) && "Query".equals(field.getParentObject().getName()) ?
                                arg.getType().formatOutput() : arg.getType().formatInput(),
                        arg.getName(),
                        Modifier.PRIVATE).build())
                .toList();
        fieldArgumentsClassBuilder.addFields(optionalArgFields);

        List<MethodSpec> optionalArgFieldWithMethods = field.getOptionalArgs().stream()
                .map(arg -> withMethod(
                        arg.getName(),
                        "id".equals(arg.getName()) && "Query".equals(field.getParentObject().getName()) ?
                                arg.getType().formatOutput() : arg.getType().formatInput(),
                        ClassName.bestGuess(fieldArgumentsClassName),
                        arg.getDescription()))
                .toList();
        fieldArgumentsClassBuilder.addMethods(optionalArgFieldWithMethods);

        List<CodeBlock> blocks = field.getOptionalArgs().stream()
                .map(arg -> CodeBlock.builder()
                        .beginControlFlow("if ($1L != null)", arg.getName())
                        .addStatement("builder.add($1S, this.$1L)", arg.getName())
                        .endControlFlow()
                        .build()).toList();
        MethodSpec toArguments = MethodSpec.methodBuilder("toArguments")
                .returns(ClassName.bestGuess("Arguments"))
                .addStatement("Arguments.Builder builder = Arguments.newBuilder()")
                .addCode(CodeBlock.join(blocks, "\n"))
                .addStatement("\nreturn builder.build()")
                .build();
        fieldArgumentsClassBuilder.addMethod(toArguments);
        fieldArgumentsClassBuilder.addJavadoc("Optional arguments for {@link $L#$L}\n\n", ClassName.bestGuess(Helpers.formatName(type)), formatName(field));
        // fieldArgumentsClassBuilder.addJavadoc("@see $T", ClassName.bestGuess(fieldArgumentsBuilderClassName));
        classBuilder.addType(fieldArgumentsClassBuilder.build());
    }
}
