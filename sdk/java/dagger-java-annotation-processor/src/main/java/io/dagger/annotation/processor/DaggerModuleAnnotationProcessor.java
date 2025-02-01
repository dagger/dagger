package io.dagger.annotation.processor;

import com.github.javaparser.StaticJavaParser;
import com.github.javaparser.javadoc.Javadoc;
import com.github.javaparser.javadoc.JavadocBlockTag;
import com.github.javaparser.javadoc.JavadocBlockTag.Type;
import com.google.auto.service.AutoService;
import com.palantir.javapoet.*;
import io.dagger.client.*;
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
import java.lang.reflect.Method;
import java.util.*;
import java.util.HashSet;
import java.util.List;
import java.util.Optional;
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
import javax.lang.model.element.Element;
import javax.lang.model.element.ElementKind;
import javax.lang.model.element.ExecutableElement;
import javax.lang.model.element.TypeElement;
import javax.lang.model.type.TypeKind;
import javax.lang.model.type.TypeMirror;
import javax.lang.model.util.Elements;

@SupportedAnnotationTypes({
  "io.dagger.module.annotation.Module",
  "io.dagger.module.annotation.Object",
  "io.dagger.module.annotation.Function",
  "io.dagger.module.annotation.Optional"
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

    for (TypeElement annotation : annotations) {
      for (Element element : roundEnv.getElementsAnnotatedWith(annotation)) {
        if (element.getKind() == ElementKind.PACKAGE) {
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

                        List<ParameterInfo> parameterInfos =
                            ((ExecutableElement) elt)
                                .getParameters().stream()
                                    .map(
                                        param -> {
                                          var optional =
                                              param.getAnnotation(
                                                  io.dagger.module.annotation.Optional.class);
                                          var isOptional = optional != null;
                                          TypeMirror tm = param.asType();
                                          TypeKind tk = tm.getKind();

                                          String defaultValue =
                                              isOptional
                                                  ? quoteIfString(
                                                      optional.defaultValue(), tm.toString())
                                                  : "";
                                          String paramName = param.getSimpleName().toString();
                                          return new ParameterInfo(
                                              paramName,
                                              parseParameterDescription(elt, paramName),
                                              new TypeInfo(tm.toString(), tk.name()),
                                              isOptional,
                                              defaultValue);
                                        })
                                    .toList();

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
                  functionInfos.toArray(new FunctionInfo[functionInfos.size()])));
        }
      }
    }

    return new ModuleInfo(
        moduleDescription, annotatedObjects.toArray(new ObjectInfo[annotatedObjects.size()]));
  }

  static String quoteIfString(String value, String type) {
    if (type.equals(String.class.getName())
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
              .addCode("\n                dag.function($S,", fnInfo.name())
              .addCode("\n                    ")
              .addCode(typeDef(fnInfo.returnType()))
              .addCode(")");
          if (isNotBlank(fnInfo.description())) {
            rm.addCode("\n                    .withDescription($S)", fnInfo.description());
          }
          for (var parameterInfo : fnInfo.parameters()) {
            rm.addCode("\n                    .withArg($S, ", parameterInfo.name())
                .addCode(typeDef(parameterInfo.type()))
                .addCode(".withOptional($L)", parameterInfo.optional());
            boolean hasDescription = isNotBlank(parameterInfo.description());
            boolean hasDefaultValue =
                parameterInfo.optional() && isNotBlank(parameterInfo.defaultValue());
            if (hasDescription || parameterInfo.optional()) {
              rm.addCode(", new $T.WithArgArguments()", io.dagger.client.Function.class);
              if (hasDescription) {
                rm.addCode(".withDescription($S)", parameterInfo.description());
              }
              if (hasDefaultValue) {
                rm.addCode(
                    ".withDefaultValue($T.from($S))", JSON.class, parameterInfo.defaultValue());
              }
            }
            rm.addCode(")");
          }
          rm.addCode(")"); // end of .withFunction(
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
        im.addStatement("$T clazz = Class.forName($S)", Class.class, objectInfo.qualifiedName())
            .addStatement("var obj = $T.fromJSON(dag, parentJson, clazz)", JsonConverter.class)
            .addStatement(
                "clazz.getMethod(\"setClient\", $T.class).invoke(obj, dag)", Client.class);
        var firstFn = true;
        for (var fnInfo : objectInfo.functions()) {
          CodeBlock fnReturnType = typeName(fnInfo.returnType());
          if (firstFn) {
            firstFn = false;
            im.beginControlFlow("if (fnName.equals($S))", fnInfo.name());
          } else {
            im.nextControlFlow("else if (fnName.equals($S))", fnInfo.name());
          }
          var fnBlock =
              CodeBlock.builder().add("$T fn = clazz.getMethod($S", Method.class, fnInfo.qName());
          var invokeBlock =
              CodeBlock.builder()
                  .add(fnReturnType)
                  .add(" res = (")
                  .add(fnReturnType)
                  .add(") fn.invoke(obj");
          for (var parameterInfo : fnInfo.parameters()) {
            CodeBlock paramType = typeName(parameterInfo.type());
            fnBlock.add(", ").add(paramType).add(".class");

            invokeBlock.add(", $L", parameterInfo.name());

            String defaultValue = "null";
            if (parameterInfo.type().kindName().equals(TypeKind.INT.name())) {
              defaultValue = "0";
            } else if (parameterInfo.type().kindName().equals(TypeKind.BOOLEAN.name())) {
              defaultValue = "false";
            }
            im.addStatement(
                CodeBlock.builder()
                    .add(paramType)
                    .add(" $L = $L", parameterInfo.name(), defaultValue)
                    .build());
            im.beginControlFlow("if (inputArgs.get($S) != null)", parameterInfo.name());
            im.addStatement(
                CodeBlock.builder()
                    .add("$L = (", parameterInfo.name())
                    .add(paramType)
                    .add(
                        ") $T.fromJSON(dag, inputArgs.get($S), ",
                        JsonConverter.class,
                        parameterInfo.name())
                    .add(paramType)
                    .add(".class)")
                    .build());
            im.endControlFlow();
          }
          fnBlock.add(")");
          invokeBlock.add(")");
          im.addStatement(fnBlock.build()).addStatement(invokeBlock.build());
          im.addStatement("return $T.toJSON(res)", JsonConverter.class);
        }
        if (!firstFn) {
          im.endControlFlow(); // functions
        }
      }
      im.endControlFlow(); // objects
      im.endControlFlow() // try json
          .addStatement("return null");

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
    }

    try {
      var clazz = Class.forName(name);
      if (clazz.isEnum()) {
        String typeName = name.substring(name.lastIndexOf('.') + 1);
        return CodeBlock.of("dag.typeDef().withEnum($S)", typeName);
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

  private String parseTypeDescription(Element element) {
    String javadocString = elementUtils.getDocComment(element);
    if (javadocString == null) {
      return element.getAnnotation(Object.class).description();
    }
    return StaticJavaParser.parseJavadoc(javadocString).getDescription().toText().trim();
  }

  private String parseFunctionDescription(Element element) {
    String javadocString = elementUtils.getDocComment(element);
    if (javadocString == null) {
      return element.getAnnotation(Function.class).description();
    }
    Javadoc javadoc = StaticJavaParser.parseJavadoc(javadocString);
    return javadoc.getDescription().toText().trim();
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
}
