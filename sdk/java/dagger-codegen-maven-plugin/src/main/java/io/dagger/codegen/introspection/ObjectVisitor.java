package io.dagger.codegen.introspection;

import static org.apache.commons.lang3.StringUtils.capitalize;

import com.palantir.javapoet.*;
import jakarta.json.bind.annotation.JsonbTypeDeserializer;
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

    // Add implements for any interfaces this object implements
    for (String ifaceName : type.getImplementedInterfaceNames()) {
      classBuilder.addSuperinterface(ClassName.bestGuess(ifaceName));
    }

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
      MethodSpec closeMethod =
          MethodSpec.methodBuilder("close")
              .addException(Exception.class)
              .addModifiers(Modifier.PUBLIC)
              .addStatement("this.connection.close()")
              .build();
      classBuilder.addMethod(closeMethod);

      // loadObjectFromID: load any object by its ID using node(id:) + inline fragment
      classBuilder.addMethod(
          MethodSpec.methodBuilder("loadObjectFromID")
              .addModifiers(Modifier.PUBLIC)
              .addTypeVariable(TypeVariableName.get("T"))
              .returns(TypeVariableName.get("T"))
              .addParameter(
                  ParameterizedTypeName.get(ClassName.get(Class.class), TypeVariableName.get("T")),
                  "clazz")
              .addParameter(ClassName.bestGuess("ID"), "id")
              .addJavadoc("Load any object by its ID using node(id:) with an inline fragment.\n")
              .beginControlFlow("try")
              .addStatement(
                  "QueryBuilder qb = this.queryBuilder.chainNode(clazz.getSimpleName(), id)")
              .addStatement(
                  "return clazz.getDeclaredConstructor(QueryBuilder.class).newInstance(qb)")
              .nextControlFlow("catch (Exception e)")
              .addStatement("throw new RuntimeException(\"Failed to load object from ID\", e)")
              .endControlFlow()
              .build());

      // nodeQueryBuilder: create a QueryBuilder for node(id:) + inline fragment
      classBuilder.addMethod(
          MethodSpec.methodBuilder("nodeQueryBuilder")
              .addModifiers(Modifier.PUBLIC)
              .returns(ClassName.bestGuess("QueryBuilder"))
              .addParameter(ClassName.get(String.class), "typeName")
              .addParameter(ClassName.bestGuess("ID"), "id")
              .addJavadoc(
                  "Create a QueryBuilder for node(id:) scoped to the given type via an inline fragment.\n")
              .addStatement("return this.queryBuilder.chainNode(typeName, id)")
              .build());
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
        // With unified IDs, id() returns the ID scalar type
        classBuilder.addSuperinterface(
            ParameterizedTypeName.get(ClassName.bestGuess("IDAble"), ClassName.bestGuess("ID")));
        classBuilder.addAnnotation(
            AnnotationSpec.builder(JsonbTypeSerializer.class)
                .addMember("value", "$T.class", ClassName.bestGuess("IDAbleSerializer"))
                .build());
        classBuilder.addAnnotation(
            AnnotationSpec.builder(JsonbTypeDeserializer.class)
                .addMember(
                    "value",
                    "$T.class",
                    ClassName.bestGuess(Helpers.formatName(type) + ".Deserializer"))
                .build());
        classBuilder.addType(
            TypeSpec.classBuilder("Deserializer")
                .addModifiers(Modifier.PUBLIC, Modifier.STATIC)
                .addSuperinterface(
                    ParameterizedTypeName.get(
                        ClassName.get(JsonbDeserializer.class),
                        ClassName.bestGuess(Helpers.formatName(type))))
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
                            "$T o = new $T($T.dag().nodeQueryBuilder($S, new $T(id)))",
                            ClassName.bestGuess(Helpers.formatName(type)),
                            ClassName.bestGuess(Helpers.formatName(type)),
                            ClassName.bestGuess("io.dagger.client.Dagger"),
                            type.getName(),
                            ClassName.bestGuess("ID"))
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

  private TypeName resolveArgType(InputObject arg, Field field) {
    // For Query.node(id: ID!), keep as raw ID scalar type
    if ("Query".equals(field.getParentObject().getName()) && "id".equals(arg.getName())) {
      return arg.getType().formatOutput();
    }
    String expectedType = arg.getExpectedType();
    return arg.getType().formatInput(expectedType);
  }

  private TypeName resolveReturnType(Field field) {
    if ("id".equals(field.getName())) {
      // id() field: with unified IDs, returns String
      return field.getTypeRef().formatOutput();
    }
    if (Helpers.isIdToConvert(field)) {
      // sync-like fields: return the parent object type
      return ClassName.bestGuess(Helpers.formatName(field.getParentObject()));
    }
    String expectedType = field.getExpectedType();
    return field.getTypeRef().formatInput(expectedType);
  }

  private void buildFieldMethod(
      TypeSpec.Builder classBuilder, Field field, boolean withOptionalArgs) {
    MethodSpec.Builder fieldMethodBuilder =
        MethodSpec.methodBuilder(Helpers.formatName(field)).addModifiers(Modifier.PUBLIC);
    TypeName returnType = resolveReturnType(field);
    fieldMethodBuilder.returns(returnType);
    List<ParameterSpec> mandatoryParams =
        field.getRequiredArgs().stream()
            .map(
                arg ->
                    ParameterSpec.builder(resolveArgType(arg, field), Helpers.formatName(arg))
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
      // For interface list elements, use the client class
      String clientClassName =
          field.getTypeRef().getListElementType().isInterface() ? objName + "Client" : objName;
      fieldMethodBuilder.addStatement(
          "nextQueryBuilder = nextQueryBuilder.chain(List.of($S))", "id");
      fieldMethodBuilder.addStatement(
          "List<QueryBuilder> builders = nextQueryBuilder.executeObjectListQuery($S)", objName);
      fieldMethodBuilder.addStatement(
          "return builders.stream().map(qb -> new $L(qb)).toList()", clientClassName);
      fieldMethodBuilder
          .addException(InterruptedException.class)
          .addException(ExecutionException.class)
          .addException(ClassName.get("io.dagger.client.exception", "DaggerQueryException"));
    } else if (field.getTypeRef().isList()) {
      fieldMethodBuilder.addStatement(
          "return nextQueryBuilder.executeListQuery($L.class)",
          field.getTypeRef().getListElementType().getName());
      fieldMethodBuilder
          .addException(InterruptedException.class)
          .addException(ExecutionException.class)
          .addException(ClassName.get("io.dagger.client.exception", "DaggerQueryException"));
    } else if (Helpers.isIdToConvert(field)) {
      fieldMethodBuilder.addStatement("nextQueryBuilder.executeQuery()");
      fieldMethodBuilder.addStatement("return this");
      fieldMethodBuilder
          .addException(InterruptedException.class)
          .addException(ExecutionException.class)
          .addException(ClassName.get("io.dagger.client.exception", "DaggerQueryException"));
    } else if (field.getTypeRef().isObjectOrInterface()) {
      // For interface return types, instantiate the client class
      if (field.getTypeRef().isInterface()) {
        String ifaceName = field.getTypeRef().getTypeName();
        fieldMethodBuilder.addStatement("return new $LClient(nextQueryBuilder)", ifaceName);
      } else {
        fieldMethodBuilder.addStatement("return new $L(nextQueryBuilder)", returnType);
      }
    } else {
      fieldMethodBuilder.addStatement("return nextQueryBuilder.executeQuery($L.class)", returnType);
      fieldMethodBuilder
          .addException(InterruptedException.class)
          .addException(ExecutionException.class)
          .addException(ClassName.get("io.dagger.client.exception", "DaggerQueryException"));
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
                            resolveArgType(arg, field), Helpers.formatName(arg), Modifier.PRIVATE)
                        .build())
            .toList();
    fieldArgumentsClassBuilder.addFields(optionalArgFields);

    List<MethodSpec> optionalArgFieldWithMethods =
        field.getOptionalArgs().stream()
            .map(
                arg ->
                    Helpers.withSetter(
                        arg,
                        resolveArgType(arg, field),
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
    classBuilder.addType(fieldArgumentsClassBuilder.build());
  }
}
