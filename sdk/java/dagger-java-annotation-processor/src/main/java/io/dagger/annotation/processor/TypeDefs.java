package io.dagger.annotation.processor;

import static io.dagger.annotation.processor.ProcessorTools.isNotBlank;

import com.google.auto.service.AutoService;
import com.palantir.javapoet.*;
import io.dagger.client.*;
import io.dagger.client.exception.DaggerExecException;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.info.FunctionInfo;
import io.dagger.module.info.ModuleInfo;
import io.dagger.module.info.ObjectInfo;
import jakarta.json.Json;
import jakarta.json.JsonArray;
import jakarta.json.JsonObject;
import java.io.IOException;
import java.io.Writer;
import java.lang.reflect.InvocationTargetException;
import java.util.*;
import java.util.List;
import java.util.Set;
import java.util.concurrent.ExecutionException;
import javax.annotation.processing.AbstractProcessor;
import javax.annotation.processing.ProcessingEnvironment;
import javax.annotation.processing.Processor;
import javax.annotation.processing.RoundEnvironment;
import javax.annotation.processing.SupportedAnnotationTypes;
import javax.annotation.processing.SupportedSourceVersion;
import javax.lang.model.SourceVersion;
import javax.lang.model.element.*;
import javax.lang.model.element.TypeElement;
import javax.lang.model.util.Elements;
import javax.tools.FileObject;
import javax.tools.StandardLocation;

@SupportedAnnotationTypes({
  "io.dagger.module.annotation.Module",
  "io.dagger.module.annotation.Object",
  "io.dagger.module.annotation.Enum",
  "io.dagger.module.annotation.Function",
  "io.dagger.module.annotation.Optional",
  "io.dagger.module.annotation.Default",
  "io.dagger.module.annotation.DefaultPath"
})
@SupportedSourceVersion(SourceVersion.RELEASE_17)
@AutoService(Processor.class)
public class TypeDefs extends AbstractProcessor {

  private Elements elementUtils;

  @Override
  public synchronized void init(ProcessingEnvironment processingEnv) {
    super.init(processingEnv);
    this.elementUtils = processingEnv.getElementUtils(); // Récupération d'Elements
  }

  private static MethodSpec.Builder registerFunction(ModuleInfo moduleInfo)
      throws ClassNotFoundException {
    var rm =
        MethodSpec.methodBuilder("register")
            .addModifiers(Modifier.STATIC)
            .returns(ModuleID.class)
            .addException(ExecutionException.class)
            .addException(DaggerQueryException.class)
            .addException(InterruptedException.class)
            .addCode("$T module = $T.dag().module()", io.dagger.client.Module.class, Dagger.class);
    if (isNotBlank(moduleInfo.description())) {
      rm.addCode("\n    .withDescription($S)", moduleInfo.description());
    }
    for (var objectInfo : moduleInfo.objects()) {
      rm.addCode("\n    .withObject(")
          .addCode("\n        $T.dag().typeDef().withObject($S", Dagger.class, objectInfo.name());
      if (isNotBlank(objectInfo.description())) {
        rm.addCode(
            ", new $T.WithObjectArguments().withDescription($S)",
            TypeDef.class,
            objectInfo.description());
      }
      rm.addCode(")"); // end of dag().TypeDef().withObject(
      for (var fnInfo : objectInfo.functions()) {
        rm.addCode("\n            .withFunction(")
            .addCode(withFunction(objectInfo, fnInfo))
            .addCode(")"); // end of .withFunction(
      }
      for (var fieldInfo : objectInfo.fields()) {
        rm.addCode("\n            .withField(")
            .addCode("$S, ", fieldInfo.name())
            .addCode(DaggerType.of(fieldInfo.type()).toDaggerTypeDef());
        if (isNotBlank(fieldInfo.description())) {
          rm.addCode(", new $T.WithFieldArguments()", TypeDef.class)
              .addCode(".withDescription($S)", fieldInfo.description());
        }
        rm.addCode(")");
      }
      if (objectInfo.constructor().isPresent()) {
        rm.addCode("\n            .withConstructor(")
            .addCode(withFunction(objectInfo, objectInfo.constructor().get()))
            .addCode(")"); // end of .withConstructor
      }
      rm.addCode(")"); // end of .withObject(
    }
    for (var enumInfo : moduleInfo.enumInfos().values()) {
      rm.addCode("\n    .withEnum(")
          .addCode("\n        $T.dag().typeDef().withEnum($S", Dagger.class, enumInfo.name());
      if (isNotBlank(enumInfo.description())) {
        rm.addCode(
            ", new $T.WithEnumArguments().withDescription($S)",
            TypeDef.class,
            enumInfo.description());
      }
      rm.addCode(")"); // end of dag().TypeDef().withEnum(
      for (var enumValue : enumInfo.values()) {
        rm.addCode("\n            .withEnumValue($S", enumValue.value());
        if (isNotBlank(enumValue.description())) {
          rm.addCode(
              ", new $T.WithEnumValueArguments().withDescription($S)",
              TypeDef.class,
              enumValue.description());
        }
        rm.addCode(")"); // end of .withEnumValue(
      }
      rm.addCode(")"); // end of .withEnum(
    }
    rm.addCode(";\n") // end of module instantiation
        .addStatement("return module.id()");

    return rm;
  }

  private static JsonObject generateTypeDefsJson(ModuleInfo moduleInfo) {
    var builder = Json.createObjectBuilder();

    // Add basic module info
    builder.add("name", "");
    builder.add("originalName", "");
    if (isNotBlank(moduleInfo.description())) {
      builder.add("description", moduleInfo.description());
    }

    // Add empty interfaces array (not used in current implementation)
    builder.add("interfaces", Json.createArrayBuilder().build());

    // Build enums array
    var enumsBuilder = Json.createArrayBuilder();
    moduleInfo
        .enumInfos()
        .values()
        .forEach(
            enumInfo -> {
              var enumBuilder = Json.createObjectBuilder().add("name", enumInfo.name());
              if (isNotBlank(enumInfo.description())) {
                enumBuilder.add("description", enumInfo.description());
              }
              var valuesBuilder = Json.createArrayBuilder();
              Arrays.stream(enumInfo.values())
                  .forEach(
                      value -> {
                        var valueBuilder = Json.createObjectBuilder().add("value", value.value());
                        if (isNotBlank(value.description())) {
                          valueBuilder.add("description", value.description());
                        }
                        valuesBuilder.add(valueBuilder);
                      });
              enumBuilder.add("values", valuesBuilder);
              enumsBuilder.add(enumBuilder);
            });
    builder.add("enums", enumsBuilder);

    // Build objects array
    var objectsBuilder = Json.createArrayBuilder();
    Arrays.stream(moduleInfo.objects())
        .forEach(
            objectInfo -> {
              var objectBuilder = Json.createObjectBuilder();
              var valuesBuilder =
                  Json.createObjectBuilder()
                      .add("Name", objectInfo.name())
                      .add("OriginalName", objectInfo.name())
                      .add("SourceModuleName", "");

              if (isNotBlank(objectInfo.description())) {
                valuesBuilder.add("Description", objectInfo.description());
              } else {
                valuesBuilder.add("Description", "");
              }

              // Add constructor if present
              if (objectInfo.constructor().isPresent()) {
                valuesBuilder.add(
                    "Constructor",
                    generateFunctionJson(objectInfo, objectInfo.constructor().get()));
              } else {
                valuesBuilder.add("Constructor", JsonObject.NULL);
              }

              // Add fields
              var fieldsBuilder = Json.createArrayBuilder();
              Arrays.stream(objectInfo.fields())
                  .forEach(
                      field -> {
                        var fieldBuilder =
                            Json.createObjectBuilder()
                                .add("name", field.name())
                                .add(
                                    "type",
                                    generateTypeDefJson(DaggerType.of(field.type()), false));
                        if (isNotBlank(field.description())) {
                          fieldBuilder.add("description", field.description());
                        }
                        fieldsBuilder.add(fieldBuilder);
                      });
              valuesBuilder.add("Fields", fieldsBuilder);

              // Add functions
              var functionsBuilder = Json.createArrayBuilder();
              Arrays.stream(objectInfo.functions())
                  .forEach(
                      function -> {
                        functionsBuilder.add(generateFunctionJson(objectInfo, function));
                      });
              valuesBuilder.add("Functions", functionsBuilder);

              valuesBuilder.add("SourceMap", JsonObject.NULL);

              objectBuilder
                  .add("kind", "OBJECT_KIND")
                  .add("optional", false)
                  .add("values", valuesBuilder);

              objectsBuilder.add(objectBuilder);
            });
    builder.add("objects", objectsBuilder);

    return builder.build();
  }

  private static JsonObject generateFunctionJson(ObjectInfo parentObject, FunctionInfo function) {
    var functionBuilder =
        Json.createObjectBuilder()
            .add("Name", function.name())
            .add("OriginalName", function.qName())
            .add("ParentOriginalName", parentObject.name());

    // Add description if present
    if (isNotBlank(function.description())) {
      functionBuilder.add("Description", function.description());
    } else {
      functionBuilder.add("Description", "");
    }

    // Add return type
    functionBuilder.add(
        "ReturnType", generateTypeDefJson(DaggerType.of(function.returnType()), false));

    // Add arguments
    var argsBuilder = Json.createArrayBuilder();
    Arrays.stream(function.parameters())
        .forEach(
            param -> {
              var argBuilder =
                  Json.createObjectBuilder()
                      .add("Name", param.name())
                      .add("OriginalName", param.name())
                      .add(
                          "TypeDef",
                          generateTypeDefJson(DaggerType.of(param.type()), param.optional()));

              if (isNotBlank(param.description())) {
                argBuilder.add("Description", param.description());
              } else {
                argBuilder.add("Description", "");
              }

              // Add default value if present
              if (param.defaultValue().isPresent()) {
                argBuilder.add("DefaultValue", param.defaultValue().get());
              } else {
                argBuilder.add("DefaultValue", JsonObject.NULL);
              }

              // Add default path if present
              if (param.defaultPath().isPresent()) {
                argBuilder.add("DefaultPath", param.defaultPath().get());
              } else {
                argBuilder.add("DefaultPath", "");
              }

              // Add ignore if present
              if (param.ignore().isPresent()) {
                var ignoreBuilder = Json.createArrayBuilder();
                Arrays.stream(param.ignore().get()).forEach(ignoreBuilder::add);
                argBuilder.add("Ignore", ignoreBuilder);
              } else {
                argBuilder.add("Ignore", JsonArray.EMPTY_JSON_ARRAY);
              }

              argBuilder.add("SourceMap", JsonObject.NULL);

              argsBuilder.add(argBuilder);
            });
    functionBuilder.add("Args", argsBuilder);

    functionBuilder.add("SourceMap", JsonObject.NULL);

    return functionBuilder.build();
  }

  private static JsonObject generateTypeDefJson(DaggerType type, boolean isOptional) {
    var typeDefBuilder =
        Json.createObjectBuilder().add("kind", type.toKind()).add("optional", isOptional);

    if (type.toKind().equals("OBJECT_KIND")) {
      var valuesBuilder =
          Json.createObjectBuilder()
              .add("Name", type.toName())
              .add("OriginalName", type.toName())
              .add("Description", "")
              .add("SourceModuleName", "")
              .add("Constructor", JsonObject.NULL)
              .add("Fields", Json.createArrayBuilder())
              .add("Functions", Json.createArrayBuilder())
              .add("SourceMap", JsonObject.NULL);
      typeDefBuilder.add("values", valuesBuilder);
    }

    return typeDefBuilder.build();
  }

  static JavaFile generateRegister(ModuleInfo moduleInfo) {
    try {
      return JavaFile.builder(
              "io.dagger.gen.entrypoint",
              TypeSpec.classBuilder("TypeDefs")
                  .addModifiers(Modifier.PUBLIC)
                  .addMethod(MethodSpec.constructorBuilder().build())
                  .addMethod(
                      MethodSpec.methodBuilder("main")
                          .addModifiers(Modifier.PUBLIC, Modifier.STATIC)
                          .addException(Exception.class)
                          .returns(void.class)
                          .addParameter(String[].class, "args")
                          .beginControlFlow("try")
                          .addStatement("new TypeDefs().dispatch()")
                          .nextControlFlow("finally")
                          .addStatement("$T.dag().close()", Dagger.class)
                          .endControlFlow()
                          .build())
                  .addMethod(
                      MethodSpec.methodBuilder("dispatch")
                          .addModifiers(Modifier.PRIVATE)
                          .returns(void.class)
                          .addException(Exception.class)
                          .addStatement(
                              "$T fnCall = $T.dag().currentFunctionCall()",
                              FunctionCall.class,
                              Dagger.class)
                          .beginControlFlow("try")
                          .addStatement(
                              "fnCall.returnValue($T.toJSON(register()))", JsonConverter.class)
                          .nextControlFlow("catch ($T e)", InvocationTargetException.class)
                          .addStatement(
                              "fnCall.returnError($T.dag().error(e.getTargetException().getMessage()))",
                              Dagger.class)
                          .addStatement("throw e")
                          .nextControlFlow("catch ($T e)", DaggerExecException.class)
                          .addStatement(
                              "fnCall.returnError($T.dag().error(e.getMessage())"
                                  + ".withValue(\"stdout\", $T.toJSON(e.getStdOut()))"
                                  + ".withValue(\"stderr\", $T.toJSON(e.getStdErr()))"
                                  + ".withValue(\"cmd\", $T.toJSON(e.getCmd()))"
                                  + ".withValue(\"exitCode\", $T.toJSON(e.getExitCode()))"
                                  + ".withValue(\"path\", $T.toJSON(e.getPath())))",
                              Dagger.class,
                              JsonConverter.class,
                              JsonConverter.class,
                              JsonConverter.class,
                              JsonConverter.class,
                              JsonConverter.class)
                          .addStatement("throw e")
                          .nextControlFlow("catch ($T e)", Exception.class)
                          .addStatement(
                              "fnCall.returnError($T.dag().error(e.getMessage()))", Dagger.class)
                          .addStatement("throw e")
                          .endControlFlow()
                          .build())
                  .addMethod(registerFunction(moduleInfo).build())
                  .build())
          .addFileComment("This class has been generated by dagger-java-sdk. DO NOT EDIT.")
          .indent("  ")
          .addStaticImport(Dagger.class, "dag")
          .build();
    } catch (ClassNotFoundException e) {
      throw new RuntimeException(e);
    }
  }

  public static CodeBlock withFunction(ObjectInfo objectInfo, FunctionInfo fnInfo) {
    boolean isConstructor = fnInfo.name().equals("<init>");
    CodeBlock.Builder code =
        CodeBlock.builder()
            .add(
                "\n                $T.dag().function($S,",
                Dagger.class,
                isConstructor ? "" : fnInfo.name())
            .add("\n                    ")
            .add(
                isConstructor
                    ? DaggerType.of(objectInfo.qualifiedName()).toDaggerTypeDef()
                    : DaggerType.of(fnInfo.returnType()).toDaggerTypeDef())
            .add(")");
    if (isNotBlank(fnInfo.description())) {
      code.add("\n                    .withDescription($S)", fnInfo.description());
    }
    for (var parameterInfo : fnInfo.parameters()) {
      code.add("\n                    .withArg($S, ", parameterInfo.name())
          .add(DaggerType.of(parameterInfo.type()).toDaggerTypeDef());
      if (parameterInfo.optional()) {
        code.add(".withOptional(true)");
      }
      boolean hasDescription = isNotBlank(parameterInfo.description());
      boolean hasDefaultValue = parameterInfo.defaultValue().isPresent();
      boolean hasDefaultPath = parameterInfo.defaultPath().isPresent();
      boolean hasIgnore = parameterInfo.ignore().isPresent();
      if (hasDescription || hasDefaultValue || hasDefaultPath || hasIgnore) {
        code.add(", new $T.WithArgArguments()", io.dagger.client.Function.class);
        if (hasDescription) {
          code.add(".withDescription($S)", parameterInfo.description());
        }
        if (hasDefaultValue) {
          code.add(
              ".withDefaultValue($T.from($S))", JSON.class, parameterInfo.defaultValue().get());
        }
        if (hasDefaultPath) {
          code.add(".withDefaultPath($S)", parameterInfo.defaultPath().get());
        }
        if (hasIgnore) {
          code.add(".withIgnore(").add(listOf(parameterInfo.ignore().get())).add(")");
        }
      }
      code.add(")");
    }
    return code.build();
  }

  @Override
  public boolean process(Set<? extends TypeElement> annotations, RoundEnvironment roundEnv) {
    ModuleInfo moduleInfo =
        new ProcessorTools(processingEnv, elementUtils).generateModuleInfo(annotations, roundEnv);

    if (moduleInfo.objects().length == 0) {
      return true;
    }

    DaggerType.setKnownEnums(moduleInfo.enumInfos().keySet());

    //    try {
    //      JavaFile f = generateRegister(moduleInfo);
    //      f.writeTo(processingEnv.getFiler());
    //    } catch (IOException e) {
    //      throw new RuntimeException(e);
    //    }

    JsonObject j = generateTypeDefsJson(moduleInfo);
    FileObject fo = null;
    try {
      fo =
          processingEnv
              .getFiler()
              .createResource(
                  StandardLocation.CLASS_OUTPUT, "", "io/dagger/gen/entrypoint/type-defs.json");
    } catch (IOException e) {
      throw new RuntimeException(e);
    }
    try (Writer w = fo.openWriter()) {
      w.write(j.toString());
    } catch (IOException e) {
      throw new RuntimeException(e);
    }

    return true;
  }

  private static CodeBlock listOf(String[] array) {
    return CodeBlock.builder()
        .add("$T.of(", List.class)
        .add(CodeBlock.join(Arrays.stream(array).map(s -> CodeBlock.of("$S", s)).toList(), ", "))
        .add(")")
        .build();
  }
}
