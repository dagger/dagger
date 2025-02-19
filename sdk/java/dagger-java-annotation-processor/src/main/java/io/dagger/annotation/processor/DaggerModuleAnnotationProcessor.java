package io.dagger.annotation.processor;

import com.github.javaparser.StaticJavaParser;
import com.github.javaparser.javadoc.Javadoc;
import com.github.javaparser.javadoc.JavadocBlockTag;
import com.github.javaparser.javadoc.JavadocBlockTag.Type;
import com.google.auto.service.AutoService;
import com.palantir.javapoet.*;
import io.dagger.client.*;
import io.dagger.module.AbstractModule;
import io.dagger.module.annotation.*;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Module;
import io.dagger.module.annotation.Object;
import io.dagger.module.info.*;
import io.dagger.module.info.FunctionInfo;
import io.dagger.module.info.ModuleInfo;
import io.dagger.module.info.ObjectInfo;
import io.dagger.module.info.ParameterInfo;
import jakarta.json.bind.JsonbBuilder;
import java.io.IOException;
import java.lang.reflect.InvocationTargetException;
import java.util.*;
import java.util.HashSet;
import java.util.List;
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
import javax.lang.model.element.*;
import javax.lang.model.element.Element;
import javax.lang.model.element.ElementKind;
import javax.lang.model.element.ExecutableElement;
import javax.lang.model.element.TypeElement;
import javax.lang.model.type.DeclaredType;
import javax.lang.model.type.TypeKind;
import javax.lang.model.type.TypeMirror;
import javax.lang.model.util.Elements;

@SupportedAnnotationTypes({
  "io.dagger.module.annotation.Module",
  "io.dagger.module.annotation.Object",
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
    String moduleDescription = null;
    Set<ObjectInfo> annotatedObjects = new HashSet<>();
    boolean hasModuleAnnotation = false;

    for (TypeElement annotation : annotations) {
      for (Element element : roundEnv.getElementsAnnotatedWith(annotation)) {
        if (element.getKind() == ElementKind.PACKAGE) {
          if (hasModuleAnnotation) {
            throw new IllegalStateException("Only one @Module annotation is allowed");
          }
          hasModuleAnnotation = true;
          Module module = element.getAnnotation(Module.class);
          moduleDescription = module.description();
          if (moduleDescription.isEmpty()) {
            moduleDescription = trimDoc(processingEnv.getElementUtils().getDocComment(element));
          }
        } else if (element.getKind() == ElementKind.CLASS
            || element.getKind() == ElementKind.RECORD) {
          TypeElement typeElement = (TypeElement) element;
          String qName = typeElement.getQualifiedName().toString();
          String name = typeElement.getAnnotation(Object.class).value();
          if (name.isEmpty()) {
            name = typeElement.getSimpleName().toString();
          }
          if (!element.getModifiers().contains(Modifier.PUBLIC)) {
            throw new RuntimeException(
                "The class %s must be public if annotated with @Object".formatted(qName));
          }

          if (!processingEnv
              .getTypeUtils()
              .isSubtype(
                  typeElement.getSuperclass(),
                  processingEnv
                      .getElementUtils()
                      .getTypeElement(AbstractModule.class.getName())
                      .asType())) {
            throw new RuntimeException(
                "The class %s must extend %s".formatted(qName, AbstractModule.class.getName()));
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

          Optional<? extends Element> constructorDef =
              typeElement.getEnclosedElements().stream()
                  .filter(elt -> elt.getKind() == ElementKind.CONSTRUCTOR)
                  .filter(elt -> !((ExecutableElement) elt).getParameters().isEmpty())
                  .findFirst();
          Optional<ConstructorInfo> constructorInfo =
              constructorDef.map(
                  elt ->
                      new ConstructorInfo(
                          ((ExecutableElement) elt)
                              .getParameters()
                              .get(0)
                              .asType()
                              .toString()
                              .equals("io.dagger.client.Client"),
                          new FunctionInfo(
                              "<init>",
                              "New",
                              parseFunctionDescription(elt),
                              new TypeInfo(
                                  ((ExecutableElement) elt).getReturnType().toString(),
                                  ((ExecutableElement) elt).getReturnType().getKind().name()),
                              parseParameters((ExecutableElement) elt)
                                  .toArray(new ParameterInfo[0]))));

          List<FieldInfo> fieldInfoInfos =
              typeElement.getEnclosedElements().stream()
                  .filter(elt -> elt.getKind() == ElementKind.FIELD)
                  .filter(elt -> elt.getModifiers().contains(Modifier.PUBLIC))
                  .filter(
                      elt ->
                          !elt.getModifiers().containsAll(List.of(Modifier.STATIC, Modifier.FINAL)))
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
                  parseTypeDescription(typeElement),
                  fieldInfoInfos.toArray(new FieldInfo[fieldInfoInfos.size()]),
                  functionInfos.toArray(new FunctionInfo[functionInfos.size()]),
                  constructorInfo));
        }
      }
    }

    return new ModuleInfo(
        moduleDescription, annotatedObjects.toArray(new ObjectInfo[annotatedObjects.size()]));
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
              if (tm instanceof DeclaredType dt) {
                if (processingEnv
                    .getTypeUtils()
                    .isSameType(dt.asElement().asType(), optionalType)) {
                  isOptional = true;
                  tm = dt.getTypeArguments().get(0);
                  tk = tm.getKind();
                }
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

  static JavaFile generate(ModuleInfo moduleInfo) {
    try {
      var rm =
          MethodSpec.methodBuilder("register")
              .addModifiers(Modifier.PRIVATE)
              .returns(ModuleID.class)
              .addException(ExecutionException.class)
              .addException(DaggerQueryException.class)
              .addException(InterruptedException.class)
              .addCode("$T module = dag.module()", io.dagger.client.Module.class);
      if (isNotBlank(moduleInfo.description())) {
        rm.addCode("\n    .withDescription($S)", moduleInfo.description());
      }
      for (var objectInfo : moduleInfo.objects()) {
        rm.addCode("\n    .withObject(")
            .addCode("\n        dag.typeDef().withObject($S", objectInfo.name());
        if (isNotBlank(objectInfo.description())) {
          rm.addCode(
              ", new $T.WithObjectArguments().withDescription($S)",
              TypeDef.class,
              objectInfo.description());
        }
        rm.addCode(")"); // end of dag.TypeDef().withObject(
        for (var fnInfo : objectInfo.functions()) {
          rm.addCode("\n            .withFunction(")
              .addCode(withFunction(objectInfo, fnInfo))
              .addCode(")"); // end of .withFunction(
        }
        for (var fieldInfo : objectInfo.fields()) {
          rm.addCode("\n            .withField(")
              .addCode("$S, ", fieldInfo.name())
              .addCode(typeDef(fieldInfo.type()));
          if (isNotBlank(fieldInfo.description())) {
            rm.addCode(", new $T.WithFieldArguments()", io.dagger.client.TypeDef.class)
                .addCode(".withDescription($S)", fieldInfo.description());
          }
          rm.addCode(")");
        }
        if (objectInfo.constructor().isPresent()) {
          rm.addCode("\n            .withConstructor(")
              .addCode(withFunction(objectInfo, objectInfo.constructor().get().constructor()))
              .addCode(")"); // end of .withConstructor
        }
        rm.addCode(")"); // end of .withObject(
      }
      rm.addCode(";\n") // end of module instantiation
          .addStatement("return module.id()");

      var im =
          MethodSpec.methodBuilder("invoke")
              .addModifiers(Modifier.PRIVATE)
              .returns(JSON.class)
              .addException(Exception.class)
              .addParameter(JSON.class, "parentJson")
              .addParameter(String.class, "parentName")
              .addParameter(String.class, "fnName")
              .addParameter(
                  ParameterizedTypeName.get(Map.class, String.class, JSON.class), "inputArgs")
              .beginControlFlow("try (var jsonb = $T.create())", JsonbBuilder.class);
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
                  "$T obj = ($T) $T.fromJSON(dag, parentJson, clazz)",
                  objName,
                  objName,
                  JsonConverter.class)
              .addStatement(
                  "clazz.getMethod(\"setClient\", $T.class).invoke(obj, dag)", Client.class);
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
      im.endControlFlow(); // objects
      im.endControlFlow() // try json
          .addStatement(
              "throw new $T(new $T(\"unknown function \" + fnName))",
              InvocationTargetException.class,
              java.lang.Error.class);

      var f =
          JavaFile.builder(
                  "io.dagger.gen.entrypoint",
                  TypeSpec.classBuilder("Entrypoint")
                      .addField(
                          FieldSpec.builder(Client.class, "dag", Modifier.PRIVATE, Modifier.FINAL)
                              .build())
                      .addModifiers(Modifier.PUBLIC)
                      .addMethod(
                          MethodSpec.constructorBuilder()
                              .addParameter(Client.class, "dag")
                              .addStatement("this.dag = dag")
                              .build())
                      .addMethod(
                          MethodSpec.methodBuilder("main")
                              .addModifiers(Modifier.PUBLIC, Modifier.STATIC)
                              .addException(Exception.class)
                              .returns(void.class)
                              .addParameter(String[].class, "args")
                              .beginControlFlow("try (Client dag = $T.connect())", Dagger.class)
                              .addStatement("new Entrypoint(dag).dispatch()")
                              .endControlFlow()
                              .build())
                      .addMethod(
                          MethodSpec.methodBuilder("dispatch")
                              .addModifiers(Modifier.PRIVATE)
                              .returns(void.class)
                              .addException(Exception.class)
                              .addStatement(
                                  "$T fnCall = dag.currentFunctionCall()", FunctionCall.class)
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
                              .addStatement("$T modID = register()", ModuleID.class)
                              .addStatement("result = $T.toJSON(modID)", JsonConverter.class)
                              .nextControlFlow("else")
                              .addStatement(
                                  "result = invoke(parentJson, parentName, fnName, inputArgs)")
                              .endControlFlow()
                              .addStatement("fnCall.returnValue(result)")
                              .nextControlFlow("catch ($T e)", InvocationTargetException.class)
                              .addStatement(
                                  "fnCall.returnError(dag.error(e.getTargetException().getMessage()))")
                              .addStatement("throw e")
                              .nextControlFlow("catch ($T e)", Exception.class)
                              .addStatement("fnCall.returnError(dag.error(e.getMessage()))")
                              .addStatement("throw e")
                              .endControlFlow()
                              .build())
                      .addMethod(rm.build())
                      .addMethod(im.build())
                      .build())
              .addFileComment("This class has been generated by dagger-java-sdk. DO NOT EDIT.")
              .indent("  ")
              .build();

      return f;
    } catch (ClassNotFoundException e) {
      throw new RuntimeException(e);
    }
  }

  private static CodeBlock functionInvoke(ObjectInfo objectInfo, ConstructorInfo constructorInfo) {
    return functionInvoke(
        objectInfo, constructorInfo.constructor(), constructorInfo.hasDaggerClient());
  }

  private static CodeBlock functionInvoke(ObjectInfo objectInfo, FunctionInfo fnInfo) {
    return functionInvoke(objectInfo, fnInfo, false);
  }

  private static CodeBlock functionInvoke(
      ObjectInfo objectInfo, FunctionInfo fnInfo, boolean hasDaggerClientInConstructor) {
    CodeBlock.Builder code = CodeBlock.builder();
    CodeBlock fnReturnType = typeName(fnInfo.returnType());

    ClassName objName = ClassName.bestGuess(objectInfo.qualifiedName());
    if (objectInfo.constructor().isPresent() && !fnInfo.name().equals("<init>")) {
      // the object initialization has been skipped, it has to be done here
      code.addStatement("$T clazz = Class.forName($S)", Class.class, objectInfo.qualifiedName())
          .addStatement(
              "$T obj = ($T) $T.fromJSON(dag, parentJson, clazz)",
              objName,
              objName,
              JsonConverter.class)
          .addStatement("obj.setClient(dag)");
    }

    for (var parameterInfo : fnInfo.parameters()) {
      CodeBlock paramType = typeName(parameterInfo.type());

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
                  .add("$L = (", parameterInfo.name())
                  .add(paramType)
                  .add(
                      ") $T.fromJSON(dag, inputArgs.get($S), ",
                      JsonConverter.class,
                      parameterInfo.name())
                  .add(paramType)
                  .add(".class")
                  .add(")")
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

    if (objectInfo.constructor().isPresent() && fnInfo.name().equals("<init>")) {
      code.add("$T res = new $T(", objName, objName);
      if (hasDaggerClientInConstructor) {
        code.add("dag");
        if (fnInfo.parameters().length > 0) {
          code.add(", ");
        }
      }
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
    code.addStatement("return $T.toJSON(res)", JsonConverter.class);

    return code.build();
  }

  public static CodeBlock withFunction(ObjectInfo objectInfo, FunctionInfo fnInfo)
      throws ClassNotFoundException {
    boolean isConstructor = fnInfo.name().equals("<init>");
    CodeBlock.Builder code =
        CodeBlock.builder()
            .add("\n                dag.function($S,", isConstructor ? "New" : fnInfo.name())
            .add("\n                    ")
            .add(
                isConstructor
                    ? typeDef(tiFromName(objectInfo.qualifiedName()))
                    : typeDef(fnInfo.returnType()))
            .add(")");
    if (isNotBlank(fnInfo.description())) {
      code.add("\n                    .withDescription($S)", fnInfo.description());
    }
    for (var parameterInfo : fnInfo.parameters()) {
      code.add("\n                    .withArg($S, ", parameterInfo.name())
          .add(typeDef(parameterInfo.type()));
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

    try {
      JavaFile f = generate(moduleInfo);

      f.writeTo(processingEnv.getFiler());
    } catch (IOException e) {
      throw new RuntimeException(e);
    }

    return true;
  }

  static CodeBlock typeDef(TypeInfo ti) throws ClassNotFoundException {
    String name = ti.typeName();
    if (name.equals("int")) {
      return CodeBlock.of(
          "dag.typeDef().withKind($T.$L)", TypeDefKind.class, TypeDefKind.INTEGER_KIND.name());
    } else if (name.equals("boolean")) {
      return CodeBlock.of(
          "dag.typeDef().withKind($T.$L)", TypeDefKind.class, TypeDefKind.BOOLEAN_KIND.name());
    } else if (name.startsWith("java.util.List<")) {
      name = name.substring("java.util.List<".length(), name.length() - 1);
      return CodeBlock.of("dag.typeDef().withListOf($L)", typeDef(tiFromName(name)).toString());
    } else if (!ti.kindName().isEmpty() && TypeKind.valueOf(ti.kindName()) == TypeKind.ARRAY) {
      // in that case the type name is com.example.Type[]
      // so we remove the [] to get the underlying type
      name = name.substring(0, name.length() - 2);
      return CodeBlock.of("dag.typeDef().withListOf($L)", typeDef(tiFromName(name)).toString());
    } else if (name.startsWith("java.util.Optional<")) {
      name = name.substring("java.util.Optional<".length(), name.length() - 1);
      return typeName(tiFromName(name));
    }

    try {
      var clazz = Class.forName(name);
      if (clazz.isEnum()) {
        String typeName = name.substring(name.lastIndexOf('.') + 1);
        return CodeBlock.of("dag.typeDef().withEnum($S)", typeName);
      } else if (Scalar.class.isAssignableFrom(clazz)) {
        String typeName = name.substring(name.lastIndexOf('.') + 1);
        return CodeBlock.of("dag.typeDef().withScalar($S)", typeName);
      }
    } catch (ClassNotFoundException e) {
      // we are ignoring here any ClassNotFoundException
      // not ideal but as we only use the clazz to check if it's an enum that should be good
    }

    try {
      if (name.startsWith("java.lang.")) {
        name = name.substring(name.lastIndexOf('.') + 1);
      }
      var kindName = (name + "_kind").toUpperCase();
      var kind = TypeDefKind.valueOf(kindName);
      return CodeBlock.of("dag.typeDef().withKind($T.$L)", TypeDefKind.class, kind.name());
    } catch (IllegalArgumentException e) {
      String typeName = name.substring(name.lastIndexOf('.') + 1);
      return CodeBlock.of("dag.typeDef().withObject($S)", typeName);
    }
  }

  static TypeInfo tiFromName(String name) {
    if (name.equals("int")) {
      return new TypeInfo(name, TypeKind.INT.name());
    } else if (name.equals("boolean")) {
      return new TypeInfo(name, TypeKind.BOOLEAN.name());
    } else if (name.equals("void")) {
      return new TypeInfo(name, TypeKind.VOID.name());
    } else {
      return new TypeInfo(name, "");
    }
  }

  static CodeBlock typeName(TypeInfo ti) {
    try {
      TypeKind tk = TypeKind.valueOf(ti.kindName());
      if (tk == TypeKind.INT) {
        return CodeBlock.of("$T", int.class);
      } else if (tk == TypeKind.BOOLEAN) {
        return CodeBlock.of("$T", boolean.class);
      } else if (tk == TypeKind.VOID) {
        return CodeBlock.of("$T", void.class);
      } else if (tk == TypeKind.ARRAY) {
        return CodeBlock.builder()
            .add(typeName(tiFromName(ti.typeName().substring(0, ti.typeName().length() - 2))))
            .add("[]")
            .build();
      }
    } catch (IllegalArgumentException ignored) {
    }
    String name = ti.typeName();
    if (name.startsWith("java.util.List<")) {
      return CodeBlock.of("$T", List.class);
    }
    if (name.startsWith("java.util.Optional<")) {
      return CodeBlock.of("$T", Optional.class);
    }
    try {
      Class<?> clazz = Class.forName(name);
      return CodeBlock.of("$T", clazz);
    } catch (ClassNotFoundException e) {
      return CodeBlock.of(
          "$T",
          ClassName.get(
              name.substring(0, name.lastIndexOf(".")), name.substring(name.lastIndexOf(".") + 1)));
    }
  }

  private String trimDoc(String doc) {
    if (doc == null) {
      return null;
    }
    return String.join("\n", doc.lines().map(String::trim).toList());
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

  private String parseTypeDescription(Element element) {
    String javadocString = elementUtils.getDocComment(element);
    if (javadocString != null) {
      return StaticJavaParser.parseJavadoc(javadocString).getDescription().toText().trim();
    }
    Object annotation = element.getAnnotation(Object.class);
    if (annotation != null) {
      return annotation.description();
    }
    return "";
  }

  private String parseFunctionDescription(Element element) {
    String javadocString = elementUtils.getDocComment(element);
    if (javadocString != null) {
      return StaticJavaParser.parseJavadoc(javadocString).getDescription().toText().trim();
    }
    Function annotation = element.getAnnotation(Function.class);
    if (annotation != null) {
      return annotation.description();
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
}
