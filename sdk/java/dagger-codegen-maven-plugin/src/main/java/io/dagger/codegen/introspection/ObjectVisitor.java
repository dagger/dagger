package io.dagger.codegen.introspection;

import static org.apache.commons.lang3.StringUtils.capitalize;

import com.palantir.javapoet.*;
import jakarta.json.bind.annotation.JsonbTypeSerializer;
import jakarta.json.bind.serializer.DeserializationContext;
import jakarta.json.bind.serializer.JsonbDeserializer;
import jakarta.json.stream.JsonParser;
import java.nio.charset.Charset;
import java.nio.file.Path;
import java.util.List;
import java.util.concurrent.ExecutionException;
import java.util.function.UnaryOperator;
import javax.lang.model.element.Modifier;

class ObjectVisitor extends AbstractVisitor {
  public ObjectVisitor(Schema schema, Path targetDirectory, Charset encoding) {
    super(schema, targetDirectory, encoding);
  }

  @Override
  TypeSpec generateType(Type type) {
    TypeSpec.Builder classBuilder =
        TypeSpec.classBuilder(Helpers.formatName(type))
            .addJavadoc(Helpers.escapeJavadoc(type.getDescription()))
            .addModifiers(Modifier.PUBLIC)
            .addField(
                FieldSpec.builder(
                        ClassName.bestGuess("QueryBuilder"), "queryBuilder", Modifier.PRIVATE)
                    .build());

    if ("Query".equals(type.getName())) {
      MethodSpec constructor =
          MethodSpec.constructorBuilder()
              .addParameter(
                  ClassName.bestGuess("io.dagger.client.engineconn.Connection"), "connection")
              .addStatement("this.connection = connection")
              .addStatement("this.queryBuilder = new QueryBuilder(connection.getGraphQLClient())")
              .build();
      classBuilder.addMethod(constructor);
      classBuilder.addField(
          FieldSpec.builder(
                  ClassName.bestGuess("io.dagger.client.engineconn.Connection"),
                  "connection",
                  Modifier.PRIVATE)
              .build());
      // AutoCloseable implementation
      classBuilder.addSuperinterface(AutoCloseable.class);
      MethodSpec closeMethod =
          MethodSpec.methodBuilder("close")
              .addException(Exception.class)
              .addModifiers(Modifier.PUBLIC)
              .addStatement("this.connection.close()")
              .build();
      classBuilder.addMethod(closeMethod);
    } else {
      // Object constructor for JSON deserialization
      MethodSpec constructor =
          MethodSpec.constructorBuilder()
              .addModifiers(Modifier.PROTECTED)
              .addJavadoc("Empty constructor for JSON-B deserialization")
              .build();
      classBuilder.addMethod(constructor);

      // If Object has an "id" field, implement IDAble interface
      if (type.providesId()) {
        classBuilder.addSuperinterface(
            ParameterizedTypeName.get(
                ClassName.bestGuess("IDAble"), type.getIdField().getTypeRef().formatOutput()));
        classBuilder.addAnnotation(
            AnnotationSpec.builder(JsonbTypeSerializer.class)
                .addMember("value", "$T.class", ClassName.bestGuess("IDAbleSerializer"))
                .build());
        classBuilder.addType(
            TypeSpec.classBuilder("Deserializer")
                .addModifiers(Modifier.PUBLIC, Modifier.STATIC)
                .addSuperinterface(
                    ParameterizedTypeName.get(
                        ClassName.get(JsonbDeserializer.class),
                        ClassName.bestGuess(Helpers.formatName(type))))
                .addField(ClassName.bestGuess("Client"), "dag", Modifier.PRIVATE, Modifier.FINAL)
                .addMethod(
                    MethodSpec.constructorBuilder()
                        .addParameter(ClassName.bestGuess("Client"), "dag")
                        .addStatement("this.dag = dag")
                        .build())
                .addMethod(
                    MethodSpec.methodBuilder("deserialize")
                        .addModifiers(Modifier.PUBLIC)
                        .addAnnotation(Override.class)
                        .returns(ClassName.bestGuess(Helpers.formatName(type)))
                        .addParameter(JsonParser.class, "parser")
                        .addParameter(DeserializationContext.class, "ctx")
                        .addParameter(java.lang.reflect.Type.class, "type")
                        .addStatement(
                            "$T id = ctx.deserialize($T.class, parser)", String.class, String.class)
                        .addStatement(
                            "$T o = dag.load$LFromID(new $T(id))",
                            ClassName.bestGuess(Helpers.formatName(type)),
                            Helpers.formatName(type),
                            type.getIdField().getTypeRef().formatOutput())
                        .addStatement("return o")
                        .build())
                .build());
      }

      for (Field scalarField :
          type.getFields().stream().filter(f -> f.getTypeRef().isScalar()).toList()) {
        classBuilder.addField(
            scalarField.getTypeRef().formatOutput(),
            Helpers.formatName(scalarField),
            Modifier.PRIVATE);
      }
    }

    // Object constructor for query building
    MethodSpec constructor =
        MethodSpec.constructorBuilder()
            .addParameter(ClassName.bestGuess("QueryBuilder"), "queryBuilder")
            .addCode("this.queryBuilder = queryBuilder;")
            .build();
    classBuilder.addMethod(constructor);

    for (Field field : type.getFields()) {
      if (field.hasOptionalArgs()) {
        buildFieldArgumentsHelpers(classBuilder, field, type);
        buildFieldMethod(classBuilder, field, true);
      }

      buildFieldMethod(classBuilder, field, false);
    }

    if (List.of("Container", "Directory").contains(type.getName())) {
      ClassName thisType = ClassName.bestGuess(Helpers.formatName(type));
      String argName = type.getName().toLowerCase() + "Func";
      classBuilder.addMethod(
          MethodSpec.methodBuilder("with")
              .addModifiers(Modifier.PUBLIC)
              .addParameter(
                  ParameterizedTypeName.get(ClassName.get(UnaryOperator.class), thisType), argName)
              .returns(thisType)
              .addStatement("return $L.apply(this)", argName)
              .build());
    }
    return classBuilder.build();
  }

  private void buildFieldMethod(
      TypeSpec.Builder classBuilder, Field field, boolean withOptionalArgs) {
    MethodSpec.Builder fieldMethodBuilder =
        MethodSpec.methodBuilder(Helpers.formatName(field)).addModifiers(Modifier.PUBLIC);
    TypeName returnType =
        "id".equals(field.getName())
            ? field.getTypeRef().formatOutput()
            : field.getTypeRef().formatInput();
    fieldMethodBuilder.returns(returnType);
    List<ParameterSpec> mandatoryParams =
        field.getRequiredArgs().stream()
            .map(
                arg ->
                    ParameterSpec.builder(
                            "Query".equals(field.getParentObject().getName())
                                    && "id".equals(arg.getName())
                                ? arg.getType().formatOutput()
                                : arg.getType().formatInput(),
                            Helpers.formatName(arg))
                        .addJavadoc(Helpers.escapeJavadoc(arg.getDescription()) + "\n")
                        .build())
            .toList();
    fieldMethodBuilder.addParameters(mandatoryParams);
    if (withOptionalArgs && field.hasOptionalArgs()) {
      fieldMethodBuilder.addParameter(
          ParameterSpec.builder(
                  ClassName.bestGuess(capitalize(Helpers.formatName(field)) + "Arguments"),
                  "optArgs")
              .addJavadoc("$L optional arguments\n", Helpers.formatName(field))
              .build());
    }
    fieldMethodBuilder.addJavadoc(Helpers.escapeJavadoc(field.getDescription()));
    // field.getRequiredArgs().forEach(arg -> fieldMethodBuilder.addJavadoc("\n@param $L $L",
    // arg.getName(), arg.getDescription()));

    if (field.getTypeRef().isScalar()
        && !Helpers.isIdToConvert(field)
        && !"Query".equals(field.getParentObject().getName())) {
      fieldMethodBuilder.beginControlFlow("if (this.$L != null)", Helpers.formatName(field));
      fieldMethodBuilder.addStatement("return $L", Helpers.formatName(field));
      fieldMethodBuilder.endControlFlow();
    }
    if (field.hasArgs()) {
      fieldMethodBuilder.addStatement("Arguments.Builder builder = Arguments.newBuilder()");
    }
    field
        .getRequiredArgs()
        .forEach(
            arg ->
                fieldMethodBuilder.addStatement(
                    "builder.add($1S, $2L)", arg.getName(), Helpers.formatName(arg)));
    if (field.hasArgs()) {
      fieldMethodBuilder.addStatement("Arguments fieldArgs = builder.build()");
    }
    if (withOptionalArgs && field.hasOptionalArgs()) {
      fieldMethodBuilder.addStatement("fieldArgs = fieldArgs.merge(optArgs.toArguments())");
    }
    if (field.hasArgs()) {
      fieldMethodBuilder.addStatement(
          "QueryBuilder nextQueryBuilder = this.queryBuilder.chain($S, fieldArgs)",
          field.getName());
    } else {
      fieldMethodBuilder.addStatement(
          "QueryBuilder nextQueryBuilder = this.queryBuilder.chain($S)", field.getName());
    }

    if (field.getTypeRef().isListOfObject()) {
      String objName = field.getTypeRef().getListElementType().getName();
      fieldMethodBuilder.addStatement(
          "nextQueryBuilder = nextQueryBuilder.chain(List.of($S))", "id");
      fieldMethodBuilder.addStatement(
          "List<QueryBuilder> builders = nextQueryBuilder.executeObjectListQuery($L.class)",
          objName);
      fieldMethodBuilder.addStatement(
          "return builders.stream().map(qb -> new $L(qb)).toList()", objName);
      fieldMethodBuilder
          .addException(InterruptedException.class)
          .addException(ExecutionException.class)
          .addException(ClassName.bestGuess("DaggerQueryException"));
    } else if (field.getTypeRef().isList()) {
      fieldMethodBuilder.addStatement(
          "return nextQueryBuilder.executeListQuery($L.class)",
          field.getTypeRef().getListElementType().getName());
      fieldMethodBuilder
          .addException(InterruptedException.class)
          .addException(ExecutionException.class)
          .addException(ClassName.bestGuess("DaggerQueryException"));
    } else if (Helpers.isIdToConvert(field)) {
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
      fieldMethodBuilder.addJavadoc("@deprecated $L\n", field.getDeprecationReason());
    }

    classBuilder.addMethod(fieldMethodBuilder.build());
  }

  /**
   * Builds the class containing the optional arguments.
   *
   * @param classBuilder
   * @param field
   * @param type
   */
  private void buildFieldArgumentsHelpers(TypeSpec.Builder classBuilder, Field field, Type type) {
    String fieldArgumentsClassName = capitalize(Helpers.formatName(field)) + "Arguments";

    /* Inner class XXXArguments */
    TypeSpec.Builder fieldArgumentsClassBuilder =
        TypeSpec.classBuilder(fieldArgumentsClassName)
            .addModifiers(Modifier.PUBLIC, Modifier.STATIC);
    List<FieldSpec> optionalArgFields =
        field.getOptionalArgs().stream()
            .map(
                arg ->
                    FieldSpec.builder(
                            "id".equals(arg.getName())
                                    && "Query".equals(field.getParentObject().getName())
                                ? arg.getType().formatOutput()
                                : arg.getType().formatInput(),
                            Helpers.formatName(arg),
                            Modifier.PRIVATE)
                        .build())
            .toList();
    fieldArgumentsClassBuilder.addFields(optionalArgFields);

    List<MethodSpec> optionalArgFieldWithMethods =
        field.getOptionalArgs().stream()
            .map(
                arg ->
                    Helpers.withSetter(
                        arg, // arg.getName(),
                        "id".equals(arg.getName())
                                && "Query".equals(field.getParentObject().getName())
                            ? arg.getType().formatOutput()
                            : arg.getType().formatInput(),
                        ClassName.bestGuess(fieldArgumentsClassName),
                        arg.getDescription()))
            .toList();
    fieldArgumentsClassBuilder.addMethods(optionalArgFieldWithMethods);

    List<CodeBlock> blocks =
        field.getOptionalArgs().stream()
            .map(
                arg ->
                    CodeBlock.builder()
                        .beginControlFlow("if ($1L != null)", Helpers.formatName(arg))
                        .addStatement(
                            "builder.add($1S, this.$2L)", arg.getName(), Helpers.formatName(arg))
                        .endControlFlow()
                        .build())
            .toList();
    MethodSpec toArguments =
        MethodSpec.methodBuilder("toArguments")
            .returns(ClassName.bestGuess("Arguments"))
            .addStatement("Arguments.Builder builder = Arguments.newBuilder()")
            .addCode(CodeBlock.join(blocks, "\n"))
            .addStatement("\nreturn builder.build()")
            .build();
    fieldArgumentsClassBuilder.addMethod(toArguments);
    fieldArgumentsClassBuilder.addJavadoc(
        "Optional arguments for {@link $L#$L}\n\n",
        ClassName.bestGuess(Helpers.formatName(type)),
        Helpers.formatName(field));
    // fieldArgumentsClassBuilder.addJavadoc("@see $T",
    // ClassName.bestGuess(fieldArgumentsBuilderClassName));
    classBuilder.addType(fieldArgumentsClassBuilder.build());
  }
}
