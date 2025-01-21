package io.dagger.annotation.processor;

import com.google.auto.service.AutoService;
import com.palantir.javapoet.*;
import io.dagger.client.*;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Module;
import io.dagger.module.annotation.Object;
import io.dagger.module.info.FunctionInfo;
import io.dagger.module.info.ObjectInfo;
import io.dagger.module.info.ParameterInfo;
import jakarta.json.bind.JsonbBuilder;
import java.io.IOException;
import java.lang.reflect.Method;
import java.util.*;
import java.util.concurrent.ExecutionException;
import javax.annotation.processing.*;
import javax.lang.model.SourceVersion;
import javax.lang.model.element.*;

@SupportedAnnotationTypes({
  "io.dagger.module.annotation.Module",
  "io.dagger.module.annotation.Object",
  "io.dagger.module.annotation.Function"
})
@SupportedSourceVersion(SourceVersion.RELEASE_17)
@AutoService(Processor.class)
public class DaggerModuleAnnotationProcessor extends AbstractProcessor {

  @Override
  public boolean process(Set<? extends TypeElement> annotations, RoundEnvironment roundEnv) {
    String moduleName = null, moduleDescription = null;
    Set<ObjectInfo> annotatedObjects = new HashSet<>();

    for (TypeElement annotation : annotations) {
      for (Element element : roundEnv.getElementsAnnotatedWith(annotation)) {
        if (element.getKind() == ElementKind.PACKAGE) {
          Module module = element.getAnnotation(Module.class);
          moduleName = module.value();
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
          String description = typeElement.getAnnotation(Object.class).description();
          if (description.isEmpty()) {
            description = trimDoc(processingEnv.getElementUtils().getDocComment(typeElement));
          }
          List<FunctionInfo> functionInfos =
              typeElement.getEnclosedElements().stream()
                  .filter(elt -> elt.getKind() == ElementKind.METHOD)
                  .filter(elt -> elt.getAnnotation(Function.class) != null)
                  .map(
                      elt -> {
                        Function moduleFunction = elt.getAnnotation(Function.class);
                        String fName = moduleFunction.value();
                        String fqName = ((ExecutableElement) elt).getSimpleName().toString();
                        if (fName.isEmpty()) {
                          fName = fqName;
                        }
                        String fDescription = moduleFunction.description();
                        if (fDescription.isEmpty()) {
                          fDescription =
                              trimDoc(processingEnv.getElementUtils().getDocComment(elt));
                        }
                        String returnType = ((ExecutableElement) elt).getReturnType().toString();

                        List<ParameterInfo> parameterInfos =
                            ((ExecutableElement) elt)
                                .getParameters().stream()
                                    .map(
                                        param -> {
                                          String paramName = param.getSimpleName().toString();
                                          String paramType = param.asType().toString();
                                          return new ParameterInfo(paramName, null, paramType);
                                        })
                                    .toList();

                        FunctionInfo functionInfo =
                            new FunctionInfo(
                                fName,
                                fqName,
                                fDescription,
                                returnType,
                                parameterInfos.toArray(new ParameterInfo[parameterInfos.size()]));
                        return functionInfo;
                      })
                  .toList();
          annotatedObjects.add(
              new ObjectInfo(
                  name,
                  qName,
                  description,
                  functionInfos.toArray(new FunctionInfo[functionInfos.size()])));
        }
      }
    }

    if (!annotatedObjects.isEmpty()) {
      try {
        var rm =
            MethodSpec.methodBuilder("register")
                .addModifiers(Modifier.PRIVATE)
                .returns(ModuleID.class)
                .addException(ExecutionException.class)
                .addException(DaggerQueryException.class)
                .addException(InterruptedException.class)
                .addCode("$T module = dag.module()", io.dagger.client.Module.class);
        if (isNotBlank(moduleDescription)) {
          rm.addCode("\n    .withDescription($S)", moduleDescription);
        }
        for (var objectInfo : annotatedObjects) {
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
              rm.addCode("\n                    .withArg($S,", parameterInfo.name())
                  .addCode(typeDef(parameterInfo.type()))
                  .addCode(")");
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
        for (var objectInfo : annotatedObjects) {
          if (firstObj) {
            firstObj = false;
            im.beginControlFlow("if (parentName.equals($S))", objectInfo.name());
          } else {
            im.nextControlFlow("else if (parentName.equals($S))", objectInfo.name());
          }
          im.addStatement("$T clazz = Class.forName($S)", Class.class, objectInfo.qualifiedName())
              .addStatement("var obj = $T.fromJSON(dag, parentJson, clazz)", Convert.class)
              .addStatement(
                  "clazz.getMethod(\"setClient\", $T.class).invoke(obj, dag)", Client.class);
          var firstFn = true;
          for (var fnInfo : objectInfo.functions()) {
            var fnReturnClass = Class.forName(fnInfo.returnType());
            if (firstFn) {
              firstFn = false;
              im.beginControlFlow("if (fnName.equals($S))", fnInfo.name());
            } else {
              im.nextControlFlow("else if (fnName.equals($S))", fnInfo.name());
            }
            var fnBlock =
                CodeBlock.builder().add("$T fn = clazz.getMethod($S", Method.class, fnInfo.name());
            var invokeBlock =
                CodeBlock.builder()
                    .add("$T res = ($T) fn.invoke(obj", fnReturnClass, fnReturnClass);
            for (var parameterInfo : fnInfo.parameters()) {
              Class<?> paramClazz = Class.forName(parameterInfo.type());
              fnBlock.add(", $T.class", paramClazz);

              invokeBlock.add(", $L", parameterInfo.name());

              im.addStatement(
                  "$T $L = ($T) $T.fromJSON(dag, inputArgs.get($S), Class.forName($S))",
                  paramClazz,
                  parameterInfo.name(),
                  paramClazz,
                  Convert.class,
                  parameterInfo.name(),
                  parameterInfo.type());
            }
            fnBlock.add(")");
            invokeBlock.add(")");
            im.addStatement(fnBlock.build()).addStatement(invokeBlock.build());
            if (fnInfo.returnType().startsWith("java.lang")) {
              im.addStatement("return $T.toJSON(res)", Convert.class);
            } else {
              im.addStatement("return res.id().toJSON()");
            }
          }
          if (!firstFn) {
            im.endControlFlow(); // functions
          }
        }
        if (!firstObj) {
          im.endControlFlow(); // objects
        }
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
                                    ParameterizedTypeName.get(
                                        List.class, FunctionCallArgValue.class))
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
                                .addStatement("result = modID.toJSON()")
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

        f.writeTo(processingEnv.getFiler());
      } catch (ClassNotFoundException | IOException e) {
        throw new RuntimeException(e);
      }
    }

    return true;
  }

  private CodeBlock typeDef(String name) {
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

  private String trimDoc(String doc) {
    if (doc == null) {
      return null;
    }
    return String.join("\n", doc.lines().map(String::trim).toList());
  }

  private Boolean isNotBlank(String str) {
    return str != null && !str.isBlank();
  }
}
