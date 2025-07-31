package io.dagger.annotation.processor;

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
import io.dagger.client.exception.DaggerExecException;
import io.dagger.module.info.FunctionInfo;
import io.dagger.module.info.ModuleInfo;
import io.dagger.module.info.ObjectInfo;
import java.io.IOException;
import java.lang.reflect.InvocationTargetException;
import java.util.Arrays;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.Optional;
import java.util.Set;
import java.util.stream.Collectors;
import javax.annotation.processing.AbstractProcessor;
import javax.annotation.processing.ProcessingEnvironment;
import javax.annotation.processing.Processor;
import javax.annotation.processing.RoundEnvironment;
import javax.annotation.processing.SupportedAnnotationTypes;
import javax.annotation.processing.SupportedSourceVersion;
import javax.lang.model.SourceVersion;
import javax.lang.model.element.Modifier;
import javax.lang.model.element.TypeElement;
import javax.lang.model.type.TypeKind;
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
          im.nextControlFlow("else if (fnName.equals(\"\"))");
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

  static JavaFile generate(ModuleInfo moduleInfo) {
    try {
      return JavaFile.builder(
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
                          .beginControlFlow("for ($T fnArg : fnArgs)", FunctionCallArgValue.class)
                          .addStatement("inputArgs.put(fnArg.name(), fnArg.value())")
                          .endControlFlow()
                          .addCode("\n")
                          .addStatement(
                              "$T result = invoke(parentJson, parentName, fnName, inputArgs)",
                              JSON.class)
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
                              "fnCall.returnError($T.dag().error(e.getMessage()))", Dagger.class)
                          .addStatement("throw e")
                          .endControlFlow()
                          .build())
                  .addMethod(invokeFunction(moduleInfo).build())
                  .build())
          .addFileComment("This class has been generated by dagger-java-sdk. DO NOT EDIT.")
          .indent("  ")
          .addStaticImport(Dagger.class, "dag")
          .build();
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

  public static TypeKind getTypeKind(String name) {
    try {
      return TypeKind.valueOf(name);
    } catch (IllegalArgumentException e) {
      return TypeKind.DECLARED;
    }
  }

  @Override
  public boolean process(Set<? extends TypeElement> annotations, RoundEnvironment roundEnv) {
    ModuleInfo moduleInfo =
        new ProcessorTools(processingEnv, elementUtils).generateModuleInfo(annotations, roundEnv);

    if (moduleInfo.objects().length == 0) {
      return true;
    }

    DaggerType.setKnownEnums(moduleInfo.enumInfos().keySet());

    try {
      JavaFile f = generate(moduleInfo);
      f.writeTo(processingEnv.getFiler());
    } catch (IOException e) {
      throw new RuntimeException(e);
    }

    return true;
  }
}
