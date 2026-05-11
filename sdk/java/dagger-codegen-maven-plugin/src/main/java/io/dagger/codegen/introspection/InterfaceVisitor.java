package io.dagger.codegen.introspection;

import com.palantir.javapoet.*;
import java.io.IOException;
import java.nio.charset.Charset;
import java.nio.file.Path;
import java.util.List;
import java.util.concurrent.ExecutionException;
import javax.lang.model.element.Modifier;

/**
 * Generates a Java interface and a concrete FooClient class for each GraphQL INTERFACE type. The
 * interface defines the contract, and FooClient provides a query-builder implementation for use
 * when loading from ID or returning from fields.
 */
class InterfaceVisitor extends AbstractVisitor {
  public InterfaceVisitor(Schema schema, Path targetDirectory, Charset encoding) {
    super(schema, targetDirectory, encoding);
  }

  @Override
  void visit(Type type) throws IOException {
    // Generate the interface
    TypeSpec interfaceSpec = generateType(type);
    write(interfaceSpec);

    // Generate the client implementation class
    TypeSpec clientSpec = generateClientType(type);
    write(clientSpec);
  }

  @Override
  TypeSpec generateType(Type type) {
    TypeSpec.Builder interfaceBuilder =
        TypeSpec.interfaceBuilder(Helpers.formatName(type))
            .addJavadoc(Helpers.escapeJavadoc(type.getDescription()))
            .addModifiers(Modifier.PUBLIC);

    if (type.getFields() != null) {
      for (Field field : type.getFields()) {
        MethodSpec.Builder methodBuilder =
            MethodSpec.methodBuilder(Helpers.formatName(field))
                .addModifiers(Modifier.PUBLIC, Modifier.ABSTRACT);

        TypeName returnType = resolveReturnType(field);
        methodBuilder.returns(returnType);

        // Add parameters for required args
        for (InputObject arg : field.getRequiredArgs()) {
          TypeName argType = resolveArgType(arg);
          methodBuilder.addParameter(
              ParameterSpec.builder(argType, Helpers.formatName(arg))
                  .addJavadoc(Helpers.escapeJavadoc(arg.getDescription()) + "\n")
                  .build());
        }

        // Add optional args parameter if needed
        if (field.hasOptionalArgs()) {
          // We don't add optional args overload to interfaces for simplicity
        }

        methodBuilder.addJavadoc(Helpers.escapeJavadoc(field.getDescription()));

        // Add exceptions for leaf/scalar/list fields
        if (needsExceptions(field)) {
          methodBuilder
              .addException(InterruptedException.class)
              .addException(ExecutionException.class)
              .addException(ClassName.get("io.dagger.client.exception", "DaggerQueryException"));
        }

        if (field.isDeprecated()) {
          methodBuilder.addAnnotation(Deprecated.class);
        }

        interfaceBuilder.addMethod(methodBuilder.build());
      }
    }

    return interfaceBuilder.build();
  }

  /** Generates the FooClient class that implements the Foo interface via query building. */
  private TypeSpec generateClientType(Type type) {
    String clientName = Helpers.formatName(type) + "Client";
    ClassName interfaceName = ClassName.bestGuess(Helpers.formatName(type));

    TypeSpec.Builder classBuilder =
        TypeSpec.classBuilder(clientName)
            .addJavadoc("Query-builder client implementation of {@link $T}.\n", interfaceName)
            .addModifiers(Modifier.PUBLIC)
            .addSuperinterface(interfaceName)
            .addField(
                FieldSpec.builder(
                        ClassName.bestGuess("QueryBuilder"), "queryBuilder", Modifier.PRIVATE)
                    .build());

    // Constructor
    MethodSpec constructor =
        MethodSpec.constructorBuilder()
            .addParameter(ClassName.bestGuess("QueryBuilder"), "queryBuilder")
            .addCode("this.queryBuilder = queryBuilder;")
            .build();
    classBuilder.addMethod(constructor);

    if (type.getFields() != null) {
      for (Field field : type.getFields()) {
        buildFieldMethod(classBuilder, field, false);
      }
    }

    return classBuilder.build();
  }

  private void buildFieldMethod(
      TypeSpec.Builder classBuilder, Field field, boolean withOptionalArgs) {
    MethodSpec.Builder fieldMethodBuilder =
        MethodSpec.methodBuilder(Helpers.formatName(field))
            .addModifiers(Modifier.PUBLIC)
            .addAnnotation(Override.class);

    TypeName returnType = resolveReturnType(field);
    fieldMethodBuilder.returns(returnType);

    List<ParameterSpec> mandatoryParams =
        field.getRequiredArgs().stream()
            .map(
                arg ->
                    ParameterSpec.builder(resolveArgType(arg), Helpers.formatName(arg))
                        .addJavadoc(Helpers.escapeJavadoc(arg.getDescription()) + "\n")
                        .build())
            .toList();
    fieldMethodBuilder.addParameters(mandatoryParams);
    fieldMethodBuilder.addJavadoc(Helpers.escapeJavadoc(field.getDescription()));

    // Build the query
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
      TypeName objectType = resolveReturnType(field);
      // For interface return types, instantiate the client class
      if (field.getTypeRef().isInterface()) {
        fieldMethodBuilder.addStatement(
            "return new $LClient(nextQueryBuilder)", field.getTypeRef().getTypeName());
      } else {
        fieldMethodBuilder.addStatement("return new $L(nextQueryBuilder)", objectType);
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
    }

    classBuilder.addMethod(fieldMethodBuilder.build());
  }

  private TypeName resolveReturnType(Field field) {
    if ("id".equals(field.getName())) {
      return field.getTypeRef().formatOutput();
    }
    if (Helpers.isIdToConvert(field)) {
      // sync-like: return the parent object type
      return ClassName.bestGuess(Helpers.formatName(field.getParentObject()));
    }
    String expectedType = field.getExpectedType();
    return field.getTypeRef().formatInput(expectedType);
  }

  private TypeName resolveArgType(InputObject arg) {
    String expectedType = arg.getExpectedType();
    return arg.getType().formatInput(expectedType);
  }

  private boolean needsExceptions(Field field) {
    if (field.getTypeRef().isListOfObject() || field.getTypeRef().isList()) {
      return true;
    }
    if (Helpers.isIdToConvert(field)) {
      return true;
    }
    if (field.getTypeRef().isObjectOrInterface()) {
      return false;
    }
    return true; // scalar fields need exceptions
  }
}
