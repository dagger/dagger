package io.dagger.annotation.processor;

import com.github.javaparser.StaticJavaParser;
import com.github.javaparser.javadoc.Javadoc;
import com.github.javaparser.javadoc.JavadocBlockTag;
import com.github.javaparser.javadoc.JavadocBlockTag.Type;
import com.google.auto.service.AutoService;
import com.palantir.javapoet.ClassName;
import com.palantir.javapoet.CodeBlock;
import com.palantir.javapoet.JavaFile;
import com.palantir.javapoet.MethodSpec;
import com.palantir.javapoet.ParameterizedTypeName;
import com.palantir.javapoet.TypeSpec;
import io.dagger.client.Dagger;
import io.dagger.client.FunctionCall;
import io.dagger.client.FunctionCallArgValue;
import io.dagger.client.JSON;
import io.dagger.client.JsonConverter;
import io.dagger.client.ModuleID;
import io.dagger.client.TypeDef;
import io.dagger.client.exception.DaggerExecException;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.Default;
import io.dagger.module.annotation.DefaultPath;
import io.dagger.module.annotation.Enum;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Ignore;
import io.dagger.module.annotation.Module;
import io.dagger.module.annotation.Object;
import io.dagger.module.info.EnumInfo;
import io.dagger.module.info.EnumValueInfo;
import io.dagger.module.info.FieldInfo;
import io.dagger.module.info.FunctionInfo;
import io.dagger.module.info.ModuleInfo;
import io.dagger.module.info.ObjectInfo;
import io.dagger.module.info.ParameterInfo;
import io.dagger.module.info.TypeInfo;
import java.io.IOException;
import java.lang.reflect.InvocationTargetException;
import java.util.Arrays;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Objects;
import java.util.Optional;
import java.util.Set;
import java.util.concurrent.ExecutionException;
import java.util.stream.Collectors;
import javax.annotation.processing.AbstractProcessor;
import javax.annotation.processing.ProcessingEnvironment;
import javax.annotation.processing.Processor;
import javax.annotation.processing.RoundEnvironment;
import javax.annotation.processing.SupportedAnnotationTypes;
import javax.annotation.processing.SupportedSourceVersion;
import javax.lang.model.SourceVersion;
import javax.lang.model.element.Element;
import javax.lang.model.element.ElementKind;
import javax.lang.model.element.ExecutableElement;
import javax.lang.model.element.Modifier;
import javax.lang.model.element.TypeElement;
import javax.lang.model.type.DeclaredType;
import javax.lang.model.type.TypeKind;
import javax.lang.model.type.TypeMirror;
import javax.lang.model.util.Elements;

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
public class DaggerModuleAnnotationProcessor extends AbstractProcessor {

  private Elements elementUtils;

  @Override
  public synchronized void init(ProcessingEnvironment processingEnv) {
    super.init(processingEnv);
    this.elementUtils = processingEnv.getElementUtils(); // Récupération d'Elements
  }

  ModuleInfo generateModuleInfo(Set<? extends TypeElement> annotations, RoundEnvironment roundEnv) {
    String moduleName = System.getenv("_DAGGER_JAVA_SDK_MODULE_NAME");

    String moduleDescription = null;
    Set<ObjectInfo> annotatedObjects = new HashSet<>();
    boolean hasModuleAnnotation = false;

    Map<String, EnumInfo> enumInfos = new HashMap<>();

    for (TypeElement annotation : annotations) {
      for (Element element : roundEnv.getElementsAnnotatedWith(annotation)) {
        switch (element.getKind()) {
          case ENUM -> {
            if (element.getAnnotation(Enum.class) != null) {
              String qName = ((TypeElement) element).getQualifiedName().toString();
              if (!enumInfos.containsKey(qName)) {
                enumInfos.put(
                    qName,
                    new EnumInfo(
                        element.getSimpleName().toString(),
                        parseJavaDocDescription(element),
                        element.getEnclosedElements().stream()
                            .filter(elt -> elt.getKind() == ElementKind.ENUM_CONSTANT)
                            .map(
                                elt ->
                                    new EnumValueInfo(
                                        elt.getSimpleName().toString(),
                                        parseJavaDocDescription(elt)))
                            .toArray(EnumValueInfo[]::new)));
              }
            }
          }
          case PACKAGE -> {
            if (hasModuleAnnotation) {
              throw new IllegalStateException("Only one @Module annotation is allowed");
            }
            hasModuleAnnotation = true;
            moduleDescription = parseModuleDescription(element);
          }
          case CLASS, RECORD -> {
            TypeElement typeElement = (TypeElement) element;
            String qName = typeElement.getQualifiedName().toString();
            String name = typeElement.getAnnotation(Object.class).value();
            if (name.isEmpty()) {
              name = typeElement.getSimpleName().toString();
            }

            boolean mainObject = areSimilar(name, moduleName);

            if (!element.getModifiers().contains(Modifier.PUBLIC)) {
              throw new RuntimeException(
                  "The class %s must be public if annotated with @Object".formatted(qName));
            }

            boolean hasDefaultConstructor =
                typeElement.getEnclosedElements().stream()
                        .filter(elt -> elt.getKind() == ElementKind.CONSTRUCTOR)
                        .map(ExecutableElement.class::cast)
                        .filter(constructor -> constructor.getModifiers().contains(Modifier.PUBLIC))
                        .anyMatch(constructor -> constructor.getParameters().isEmpty())
                    || typeElement.getEnclosedElements().stream()
                        .noneMatch(elt -> elt.getKind() == ElementKind.CONSTRUCTOR);

            if (!hasDefaultConstructor) {
              throw new RuntimeException(
                  "The class %s must have a public no-argument constructor that calls super()"
                      .formatted(qName));
            }

            Optional<FunctionInfo> constructorInfo = Optional.empty();
            if (mainObject) {
              List<? extends Element> constructorDefs =
                  typeElement.getEnclosedElements().stream()
                      .filter(elt -> elt.getKind() == ElementKind.CONSTRUCTOR)
                      .filter(elt -> !((ExecutableElement) elt).getParameters().isEmpty())
                      .toList();
              if (constructorDefs.size() == 1) {
                Element elt = constructorDefs.get(0);
                constructorInfo =
                    Optional.of(
                        new FunctionInfo(
                            "<init>",
                            "",
                            parseFunctionDescription(elt),
                            new TypeInfo(
                                ((ExecutableElement) elt).getReturnType().toString(),
                                ((ExecutableElement) elt).getReturnType().getKind().name()),
                            parseParameters((ExecutableElement) elt)
                                .toArray(new ParameterInfo[0])));
              } else if (constructorDefs.size() > 1) {
                // There's more than one non-empty constructor, but Dagger only supports to expose a
                // single one
                throw new RuntimeException(
                    "The class %s must have a single non-empty constructor".formatted(qName));
              }
            }

            List<FieldInfo> fieldInfoInfos =
                typeElement.getEnclosedElements().stream()
                    .filter(elt -> elt.getKind() == ElementKind.FIELD)
                    .filter(elt -> !elt.getModifiers().contains(Modifier.TRANSIENT))
                    .filter(elt -> !elt.getModifiers().contains(Modifier.STATIC))
                    .filter(elt -> !elt.getModifiers().contains(Modifier.FINAL))
                    .filter(
                        elt ->
                            elt.getModifiers().contains(Modifier.PUBLIC)
                                || elt.getAnnotation(Function.class) != null)
                    .map(
                        elt -> {
                          String fieldName = elt.getSimpleName().toString();
                          TypeMirror tm = elt.asType();
                          TypeKind tk = tm.getKind();
                          FieldInfo f =
                              new FieldInfo(
                                  fieldName,
                                  parseSimpleDescription(elt),
                                  new TypeInfo(tm.toString(), tk.name()));
                          return f;
                        })
                    .toList();
            List<FunctionInfo> functionInfos =
                typeElement.getEnclosedElements().stream()
                    .filter(elt -> elt.getKind() == ElementKind.METHOD)
                    .filter(elt -> elt.getAnnotation(Function.class) != null)
                    .map(
                        elt -> {
                          Function moduleFunction = elt.getAnnotation(Function.class);
                          String fName = moduleFunction.value();
                          String fqName = elt.getSimpleName().toString();
                          if (fName.isEmpty()) {
                            fName = fqName;
                          }
                          if (!elt.getModifiers().contains(Modifier.PUBLIC)) {
                            throw new RuntimeException(
                                "The method %s#%s must be public if annotated with @Function"
                                    .formatted(qName, fqName));
                          }

                          List<ParameterInfo> parameterInfos =
                              parseParameters((ExecutableElement) elt);

                          TypeMirror tm = ((ExecutableElement) elt).getReturnType();
                          TypeKind tk = tm.getKind();
                          FunctionInfo functionInfo =
                              new FunctionInfo(
                                  fName,
                                  fqName,
                                  parseFunctionDescription(elt),
                                  new TypeInfo(tm.toString(), tk.name()),
                                  parameterInfos.toArray(new ParameterInfo[parameterInfos.size()]));
                          return functionInfo;
                        })
                    .toList();
            annotatedObjects.add(
                new ObjectInfo(
                    name,
                    qName,
                    parseObjectDescription(typeElement),
                    fieldInfoInfos.toArray(new FieldInfo[fieldInfoInfos.size()]),
                    functionInfos.toArray(new FunctionInfo[functionInfos.size()]),
                    constructorInfo));
          }
        }
      }
    }

    // Ensure only one single enum is defined with a specific name
    Set<String> enumSimpleNames = new HashSet<>();
    for (var enumQualifiedName : enumInfos.keySet()) {
      String simpleName = enumQualifiedName.substring(enumQualifiedName.lastIndexOf('.') + 1);
      if (enumSimpleNames.contains(simpleName)) {
        throw new RuntimeException(
            "The enum %s has already been registered via %s"
                .formatted(simpleName, enumQualifiedName));
      }
      enumSimpleNames.add(simpleName);
    }

    return new ModuleInfo(
        moduleDescription,
        annotatedObjects.toArray(new ObjectInfo[annotatedObjects.size()]),
        enumInfos);
  }

  private List<ParameterInfo> parseParameters(ExecutableElement elt) {
    return elt.getParameters().stream()
        .filter(param -> !param.asType().toString().equals("io.dagger.client.Client"))
        .map(
            param -> {
              TypeMirror tm = param.asType();
              TypeKind tk = tm.getKind();

              boolean isOptional = false;
              var optionalType =
                  processingEnv.getElementUtils().getTypeElement(Optional.class.getName()).asType();
              if (tm instanceof DeclaredType dt
                  && processingEnv.getTypeUtils().isSameType(dt.asElement().asType(), optionalType)
                  && !dt.getTypeArguments().isEmpty()) {
                isOptional = true;
                tm = dt.getTypeArguments().get(0);
                tk = tm.getKind();
              }

              Default defaultAnnotation = param.getAnnotation(Default.class);
              var hasDefaultAnnotation = defaultAnnotation != null;

              DefaultPath defaultPathAnnotation = param.getAnnotation(DefaultPath.class);
              var hasDefaultPathAnnotation = defaultPathAnnotation != null;

              if (hasDefaultPathAnnotation
                  && !tm.toString().equals("io.dagger.client.Directory")
                  && !tm.toString().equals("io.dagger.client.File")) {
                throw new IllegalArgumentException(
                    "Parameter "
                        + param.getSimpleName()
                        + " cannot have @DefaultPath annotation if it is not a Directory or File type");
              }

              if (hasDefaultAnnotation && hasDefaultPathAnnotation) {
                throw new IllegalArgumentException(
                    "Parameter "
                        + param.getSimpleName()
                        + " cannot have both @Default and @DefaultPath annotations");
              }

              String defaultValue =
                  hasDefaultAnnotation
                      ? quoteIfString(defaultAnnotation.value(), tm.toString())
                      : null;

              String defaultPath = hasDefaultPathAnnotation ? defaultPathAnnotation.value() : null;

              Ignore ignoreAnnotation = param.getAnnotation(Ignore.class);
              var hasIgnoreAnnotation = ignoreAnnotation != null;
              if (hasIgnoreAnnotation && !tm.toString().equals("io.dagger.client.Directory")) {
                throw new IllegalArgumentException(
                    "Parameter "
                        + param.getSimpleName()
                        + " cannot have @Ignore annotation if it is not a Directory");
              }

              String[] ignoreValue = hasIgnoreAnnotation ? ignoreAnnotation.value() : null;

              String paramName = param.getSimpleName().toString();
              return new ParameterInfo(
                  paramName,
                  parseParameterDescription(elt, paramName),
                  new TypeInfo(tm.toString(), tk.name()),
                  isOptional,
                  Optional.ofNullable(defaultValue),
                  Optional.ofNullable(defaultPath),
                  Optional.ofNullable(ignoreValue));
            })
        .toList();
  }

  static String quoteIfString(String value, String type) {
    if (value == null) {
      return null;
    }
    if (type.equals(String.class.getName())
        && !value.equals("null")
        && (!value.startsWith("\"") && !value.endsWith("\"")
            || !value.startsWith("'") && !value.endsWith("'"))) {
      return "\"" + value.replaceAll("\"", "\\\\\"") + "\"";
    }
    return value;
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
            .addCode(withFunction(moduleInfo.enumInfos().keySet(), objectInfo, fnInfo))
            .addCode(")"); // end of .withFunction(
      }
      for (var fieldInfo : objectInfo.fields()) {
        rm.addCode("\n            .withField(")
            .addCode("$S, ", fieldInfo.name())
            .addCode(DaggerType.of(fieldInfo.type()).toDaggerTypeDef());
        if (isNotBlank(fieldInfo.description())) {
          rm.addCode(", new $T.WithFieldArguments()", io.dagger.client.TypeDef.class)
              .addCode(".withDescription($S)", fieldInfo.description());
        }
        rm.addCode(")");
      }
      if (objectInfo.constructor().isPresent()) {
        rm.addCode("\n            .withConstructor(")
            .addCode(
                withFunction(
                    moduleInfo.enumInfos().keySet(), objectInfo, objectInfo.constructor().get()))
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
              io.dagger.client.TypeDef.class,
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

  private static MethodSpec.Builder invokeFunction(ModuleInfo moduleInfo)
      throws ClassNotFoundException {
    var im =
        MethodSpec.methodBuilder("invoke")
            .addModifiers(Modifier.PRIVATE)
            .returns(JSON.class)
            .addException(Exception.class)
            .addParameter(JSON.class, "parentJson")
            .addParameter(String.class, "parentName")
            .addParameter(String.class, "fnName")
            .addParameter(
                ParameterizedTypeName.get(Map.class, String.class, JSON.class), "inputArgs");
    var firstObj = true;
    for (var objectInfo : moduleInfo.objects()) {
      if (firstObj) {
        firstObj = false;
        im.beginControlFlow("if (parentName.equals($S))", objectInfo.name());
      } else {
        im.nextControlFlow("else if (parentName.equals($S))", objectInfo.name());
      }
      // If there's no constructor, we can initialize the main object here as it's the same for
      // all.
      // But if there's a constructor we want to inline it under the function branch.
      if (objectInfo.constructor().isEmpty()) {
        ClassName objName = ClassName.bestGuess(objectInfo.qualifiedName());
        im.addStatement("$T clazz = Class.forName($S)", Class.class, objectInfo.qualifiedName())
            .addStatement(
                "$T obj = ($T) $T.fromJSON(parentJson, clazz)",
                objName,
                objName,
                JsonConverter.class);
      }
      var firstFn = true;
      for (var fnInfo : objectInfo.functions()) {
        if (firstFn) {
          firstFn = false;
          im.beginControlFlow("if (fnName.equals($S))", fnInfo.name());
        } else {
          im.nextControlFlow("else if (fnName.equals($S))", fnInfo.name());
        }
        im.addCode(functionInvoke(objectInfo, fnInfo));
      }

      if (objectInfo.constructor().isPresent()) {
        if (firstFn) {
          firstFn = false;
          im.beginControlFlow("if (fnName.equals(\"\"))");
        } else {
          im.nextControlFlow("if (fnName.equals(\"\"))");
        }
        im.addCode(functionInvoke(objectInfo, objectInfo.constructor().get()));
      }

      if (!firstFn) {
        im.endControlFlow(); // functions
      }
    }
    im.endControlFlow() // objects
        .addStatement(
            "throw new $T(new $T(\"unknown function \" + fnName))",
            InvocationTargetException.class,
            java.lang.Error.class);

    return im;
  }

  static JavaFile generateRegister(ModuleInfo moduleInfo) {
    try {
      var f =
          JavaFile.builder(
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
                              .nextControlFlow("catch ($T e)", Exception.class)
                              .addStatement(
                                  "fnCall.returnError($T.dag().error(e.getMessage()))",
                                  Dagger.class)
                              .addStatement("throw e")
                              .endControlFlow()
                              .build())
                      .addMethod(registerFunction(moduleInfo).build())
                      .build())
              .addFileComment("This class has been generated by dagger-java-sdk. DO NOT EDIT.")
              .indent("  ")
              .addStaticImport(Dagger.class, "dag")
              .build();

      return f;
    } catch (ClassNotFoundException e) {
      throw new RuntimeException(e);
    }
  }

  static JavaFile generate(ModuleInfo moduleInfo) {
    try {
      var f =
          JavaFile.builder(
                  "io.dagger.gen.entrypoint",
                  TypeSpec.classBuilder("Entrypoint")
                      .addModifiers(Modifier.PUBLIC)
                      .addMethod(MethodSpec.constructorBuilder().build())
                      .addMethod(
                          MethodSpec.methodBuilder("main")
                              .addModifiers(Modifier.PUBLIC, Modifier.STATIC)
                              .addException(Exception.class)
                              .returns(void.class)
                              .addParameter(String[].class, "args")
                              .beginControlFlow("try")
                              .addStatement("new Entrypoint().dispatch()")
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
                              .addStatement("$T parentName = fnCall.parentName()", String.class)
                              .addStatement("$T fnName = fnCall.name()", String.class)
                              .addStatement("$T parentJson = fnCall.parent()", JSON.class)
                              .addStatement(
                                  "$T fnArgs = fnCall.inputArgs()",
                                  ParameterizedTypeName.get(List.class, FunctionCallArgValue.class))
                              .addStatement(
                                  "$T<$T, $T> inputArgs = new $T<>()",
                                  Map.class,
                                  String.class,
                                  JSON.class,
                                  HashMap.class)
                              .beginControlFlow(
                                  "for ($T fnArg : fnArgs)", FunctionCallArgValue.class)
                              .addStatement("inputArgs.put(fnArg.name(), fnArg.value())")
                              .endControlFlow()
                              .addCode("\n")
                              .addStatement("$T result", JSON.class)
                              .beginControlFlow("if (parentName.isEmpty())")
                              .addStatement("$T modID = TypeDefs.register()", ModuleID.class)
                              .addStatement("result = $T.toJSON(modID)", JsonConverter.class)
                              .nextControlFlow("else")
                              .addStatement(
                                  "result = invoke(parentJson, parentName, fnName, inputArgs)")
                              .endControlFlow()
                              .addStatement("fnCall.returnValue(result)")
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
                                  "fnCall.returnError($T.dag().error(e.getMessage()))",
                                  Dagger.class)
                              .addStatement("throw e")
                              .endControlFlow()
                              .build())
                      .addMethod(invokeFunction(moduleInfo).build())
                      .build())
              .addFileComment("This class has been generated by dagger-java-sdk. DO NOT EDIT.")
              .indent("  ")
              .addStaticImport(Dagger.class, "dag")
              .build();

      return f;
    } catch (ClassNotFoundException e) {
      throw new RuntimeException(e);
    }
  }

  private static CodeBlock functionInvoke(ObjectInfo objectInfo, FunctionInfo fnInfo) {
    CodeBlock.Builder code = CodeBlock.builder();
    CodeBlock fnReturnType = DaggerType.of(fnInfo.returnType()).toJavaType();

    CodeBlock startAsList = CodeBlock.of("$T.asList(", Arrays.class);
    CodeBlock endAsList = CodeBlock.of(")");
    CodeBlock empty = CodeBlock.of("");

    ClassName objName = ClassName.bestGuess(objectInfo.qualifiedName());
    if (objectInfo.constructor().isPresent() && !fnInfo.name().equals("<init>")) {
      // the object initialization has been skipped, it has to be done here
      code.addStatement("$T clazz = Class.forName($S)", Class.class, objectInfo.qualifiedName())
          .addStatement(
              "$T obj = ($T) $T.fromJSON(parentJson, clazz)",
              objName,
              objName,
              JsonConverter.class);
    }

    for (var parameterInfo : fnInfo.parameters()) {
      DaggerType type = DaggerType.of(parameterInfo.type());
      CodeBlock paramType = type.toJavaType();
      CodeBlock classType = type.toClass();

      String defaultValue = "null";
      TypeKind tk = getTypeKind(parameterInfo.type().kindName());
      if (tk == TypeKind.INT
          || tk == TypeKind.LONG
          || tk == TypeKind.DOUBLE
          || tk == TypeKind.FLOAT
          || tk == TypeKind.SHORT
          || tk == TypeKind.BYTE
          || tk == TypeKind.CHAR) {
        defaultValue = "0";
      } else if (tk == TypeKind.BOOLEAN) {
        defaultValue = "false";
      }
      code.add(paramType)
          .add(" $L = $L;\n", parameterInfo.name(), defaultValue)
          .beginControlFlow("if (inputArgs.get($S) != null)", parameterInfo.name())
          .addStatement(
              CodeBlock.builder()
                  .add("$L = ", parameterInfo.name())
                  .add(type.isList() ? startAsList : empty)
                  .add("$T.fromJSON(inputArgs.get($S), ", JsonConverter.class, parameterInfo.name())
                  .add(classType)
                  .add(")")
                  .add(type.isList() ? endAsList : empty)
                  .build())
          .endControlFlow();

      if (!parameterInfo.optional() && !tk.isPrimitive()) {
        code.addStatement(
            "$T.requireNonNull($L, \"$L must not be null\")",
            Objects.class,
            parameterInfo.name(),
            parameterInfo.name());
      } else if (parameterInfo.optional()) {
        code.addStatement(
            "var $L_opt = $T.ofNullable($L)",
            parameterInfo.name(),
            Optional.class,
            parameterInfo.name());
      }
    }

    boolean returnsVoid = fnInfo.returnType().typeName().equals("void");
    boolean isConstructor = objectInfo.constructor().isPresent() && fnInfo.name().equals("<init>");
    if (isConstructor) {
      code.add("$T res = new $T(", objName, objName);
    } else if (returnsVoid) {
      code.add("obj.$L(", fnInfo.qName());
    } else {
      code.add(fnReturnType).add(" res = obj.$L(", fnInfo.qName());
    }
    code.add(
            CodeBlock.join(
                Arrays.stream(fnInfo.parameters())
                    .map(
                        p ->
                            CodeBlock.of(
                                "$L",
                                p.optional()
                                    ? CodeBlock.of("$L_opt", p.name())
                                    : CodeBlock.of("$L", p.name())))
                    .collect(Collectors.toList()),
                ", "))
        .add(");\n");
    if (returnsVoid && !isConstructor) {
      code.addStatement("return $T.toJSON(null)", JsonConverter.class);
    } else {
      code.addStatement("return $T.toJSON(res)", JsonConverter.class);
    }

    return code.build();
  }

  public static CodeBlock withFunction(
      Set<String> enums, ObjectInfo objectInfo, FunctionInfo fnInfo) throws ClassNotFoundException {
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

  public static TypeKind getTypeKind(String name) {
    try {
      TypeKind kind = TypeKind.valueOf(name);
      return kind;
    } catch (IllegalArgumentException e) {
      return TypeKind.DECLARED;
    }
  }

  @Override
  public boolean process(Set<? extends TypeElement> annotations, RoundEnvironment roundEnv) {
    ModuleInfo moduleInfo = generateModuleInfo(annotations, roundEnv);

    if (moduleInfo.objects().length == 0) {
      return true;
    }

    DaggerType.setKnownEnums(moduleInfo.enumInfos().keySet());

    try {
      JavaFile f = generate(moduleInfo);
      f.writeTo(processingEnv.getFiler());

      f = generateRegister(moduleInfo);
      f.writeTo(processingEnv.getFiler());
    } catch (IOException e) {
      throw new RuntimeException(e);
    }

    return true;
  }

  private static Boolean isNotBlank(String str) {
    return str != null && !str.isBlank();
  }

  private String parseSimpleDescription(Element element) {
    String javadocString = elementUtils.getDocComment(element);
    if (javadocString == null) {
      return "";
    }
    return StaticJavaParser.parseJavadoc(javadocString).getDescription().toText().trim();
  }

  private String parseModuleDescription(Element element) {
    Module annotation = element.getAnnotation(Module.class);
    if (annotation != null && !annotation.description().isEmpty()) {
      return annotation.description();
    }
    return parseJavaDocDescription(element);
  }

  private String parseObjectDescription(Element element) {
    Object annotation = element.getAnnotation(Object.class);
    if (annotation != null && !annotation.description().isEmpty()) {
      return annotation.description();
    }
    return parseJavaDocDescription(element);
  }

  private String parseFunctionDescription(Element element) {
    Function annotation = element.getAnnotation(Function.class);
    if (annotation != null && !annotation.description().isEmpty()) {
      return annotation.description();
    }
    return parseJavaDocDescription(element);
  }

  private String parseJavaDocDescription(Element element) {
    String javadocString = elementUtils.getDocComment(element);
    if (javadocString != null) {
      return StaticJavaParser.parseJavadoc(javadocString).getDescription().toText().trim();
    }
    return "";
  }

  private String parseParameterDescription(Element element, String paramName) {
    String javadocString = elementUtils.getDocComment(element);
    if (javadocString == null) {
      return "";
    }
    Javadoc javadoc = StaticJavaParser.parseJavadoc(javadocString);
    Optional<JavadocBlockTag> blockTag =
        javadoc.getBlockTags().stream()
            .filter(tag -> tag.getType() == Type.PARAM)
            .filter(tag -> tag.getName().isPresent() && tag.getName().get().equals(paramName))
            .findFirst();
    return blockTag.map(tag -> tag.getContent().toText()).orElse("");
  }

  private static CodeBlock listOf(String[] array) {
    return CodeBlock.builder()
        .add("$T.of(", List.class)
        .add(CodeBlock.join(Arrays.stream(array).map(s -> CodeBlock.of("$S", s)).toList(), ", "))
        .add(")")
        .build();
  }

  private static boolean areSimilar(String str1, String str2) {
    return normalize(str1).equals(normalize(str2));
  }

  private static String normalize(String str) {
    if (str == null) {
      return "";
    }
    return str.replaceAll("[-_]", " ") // Replace kebab and snake case delimiters with spaces
        .replaceAll("([a-z])([A-Z])", "$1 $2") // Split camel case words
        .toLowerCase(Locale.ROOT) // Convert to lowercase
        .replaceAll("\\s+", ""); // Remove all spaces
  }
}
