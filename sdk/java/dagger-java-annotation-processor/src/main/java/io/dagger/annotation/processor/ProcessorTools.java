package io.dagger.annotation.processor;

import com.github.javaparser.StaticJavaParser;
import com.github.javaparser.javadoc.Javadoc;
import com.github.javaparser.javadoc.JavadocBlockTag;
import io.dagger.module.annotation.*;
import io.dagger.module.annotation.Module;
import io.dagger.module.annotation.Object;
import io.dagger.module.info.*;
import java.util.*;
import javax.annotation.processing.ProcessingEnvironment;
import javax.annotation.processing.RoundEnvironment;
import javax.lang.model.element.*;
import javax.lang.model.type.DeclaredType;
import javax.lang.model.type.TypeKind;
import javax.lang.model.type.TypeMirror;
import javax.lang.model.util.Elements;

public class ProcessorTools {
  private final ProcessingEnvironment processingEnv;
  private final Elements elementUtils;

  ProcessorTools(ProcessingEnvironment processingEnv, Elements elementUtils) {
    this.processingEnv = processingEnv;
    this.elementUtils = elementUtils;
  }

  private String quoteIfString(String value, String type) {
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
            if (element.getAnnotation(io.dagger.module.annotation.Enum.class) != null) {
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
                          return new FieldInfo(
                              fieldName,
                              parseSimpleDescription(elt),
                              new TypeInfo(tm.toString(), tk.name()));
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
                          return new FunctionInfo(
                              fName,
                              fqName,
                              parseFunctionDescription(elt),
                              new TypeInfo(tm.toString(), tk.name()),
                              parameterInfos.toArray(new ParameterInfo[0]));
                        })
                    .toList();
            annotatedObjects.add(
                new ObjectInfo(
                    name,
                    qName,
                    parseObjectDescription(typeElement),
                    fieldInfoInfos.toArray(new FieldInfo[0]),
                    functionInfos.toArray(new FunctionInfo[0]),
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
        moduleDescription, annotatedObjects.toArray(new ObjectInfo[0]), enumInfos);
  }

  List<ParameterInfo> parseParameters(ExecutableElement elt) {
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

  static Boolean isNotBlank(String str) {
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
    io.dagger.module.annotation.Module annotation = element.getAnnotation(Module.class);
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
            .filter(tag -> tag.getType() == JavadocBlockTag.Type.PARAM)
            .filter(tag -> tag.getName().isPresent() && tag.getName().get().equals(paramName))
            .findFirst();
    return blockTag.map(tag -> tag.getContent().toText()).orElse("");
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
