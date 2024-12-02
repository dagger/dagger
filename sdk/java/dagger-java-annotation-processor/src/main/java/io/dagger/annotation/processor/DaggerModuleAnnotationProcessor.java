package io.dagger.annotation.processor;

import java.io.IOException;
import java.io.PrintWriter;
import java.util.HashSet;
import java.util.List;
import java.util.Set;

import javax.annotation.processing.AbstractProcessor;
import javax.annotation.processing.Processor;
import javax.annotation.processing.RoundEnvironment;
import javax.annotation.processing.SupportedAnnotationTypes;
import javax.annotation.processing.SupportedSourceVersion;
import javax.lang.model.SourceVersion;
import javax.lang.model.element.Element;
import javax.lang.model.element.ElementKind;
import javax.lang.model.element.ExecutableElement;
import javax.lang.model.element.TypeElement;
import javax.tools.FileObject;
import javax.tools.StandardLocation;

import com.google.auto.service.AutoService;

import io.dagger.module.annotation.Module;
import io.dagger.module.annotation.ModuleFunction;
import io.dagger.module.annotation.ModuleObject;
import jakarta.json.bind.Jsonb;
import jakarta.json.bind.JsonbBuilder;

@SupportedAnnotationTypes({
    "io.dagger.module.annotation.Module",
    "io.dagger.module.annotation.ModuleObject",
    "io.dagger.module.annotation.ModuleFunction"
})
@SupportedSourceVersion(SourceVersion.RELEASE_17)
@AutoService(Processor.class)
public class DaggerModuleAnnotationProcessor extends AbstractProcessor {

    @Override
    public boolean process(Set<? extends TypeElement> annotations, RoundEnvironment roundEnv) {
        String moduleName = null, moduleDescription = null;
        Set<ObjectInfo> annotatedObjects = new HashSet<>();

        System.out.println("Annotation Processor");
        for (TypeElement annotation : annotations) {
            for (Element element : roundEnv.getElementsAnnotatedWith(annotation)) {
                if (element.getKind() == ElementKind.PACKAGE) {
                    Module module = element.getAnnotation(Module.class);
                    moduleName = module.value();
                    moduleDescription = module.description();
                } else if (element.getKind() == ElementKind.CLASS || element.getKind() == ElementKind.RECORD) {
                    TypeElement typeElement = (TypeElement) element;
                    String qName = typeElement.getQualifiedName().toString();
                    String name = typeElement.getAnnotation(ModuleObject.class).value();
                    String description = typeElement.getAnnotation(ModuleObject.class).description();
                    if (name.isEmpty()) {
                        name = typeElement.getSimpleName().toString();
                    }
                    List<FunctionInfo> functionInfos = typeElement.getEnclosedElements().stream()
                        .filter(elt -> elt.getKind() == ElementKind.METHOD)
                        .filter(elt -> elt.getAnnotation(ModuleFunction.class) != null)
                        .map(elt -> {
                            ModuleFunction moduleFunction = elt.getAnnotation(ModuleFunction.class);
                            String fName = moduleFunction.value();
                            String fqName = ((ExecutableElement)elt).getSimpleName().toString();
                            String fDescription = moduleFunction.description();
                            if (fName.isEmpty()) {
                                fName = fqName;
                            }
                            String returnType = ((ExecutableElement)elt).getReturnType().toString();

                            List<ParameterInfo> parameterInfos = ((ExecutableElement)elt).getParameters().stream().map(param -> {
                                String paramName = param.getSimpleName().toString();
                                String paramType = param.asType().toString();
                                return new ParameterInfo(paramName, paramType, null);
                            }).toList();

                            FunctionInfo functionInfo = new FunctionInfo(fName, fqName, fDescription, returnType,
                                parameterInfos.toArray(new ParameterInfo[parameterInfos.size()]));
                            return functionInfo;
                        }).toList();
                   annotatedObjects.add(new ObjectInfo(name, qName, description, functionInfos.toArray(new FunctionInfo[functionInfos.size()])));
                }
            }
        }

        System.out.println(annotatedObjects);

        if (!annotatedObjects.isEmpty()) {
            try {
                FileObject resource = processingEnv.getFiler().createResource(
                    StandardLocation.CLASS_OUTPUT, "", "dagger_module_info.json");
                try (PrintWriter out = new PrintWriter(resource.openWriter())) {
                    writeModule(moduleName, moduleDescription, annotatedObjects, out);
                }
            } catch (IOException ioe) {
                throw new RuntimeException(ioe);
            }
        }

        return true;
    }

    private void writeModule(String moduleName, String moduleDescription, Set<ObjectInfo> annotatedClasses, PrintWriter out) throws IOException {
        ModuleInfo moduleInfo = new ModuleInfo(moduleName, moduleDescription, annotatedClasses.toArray(new ObjectInfo[annotatedClasses.size()]));
        Jsonb jsonb = JsonbBuilder.create();
        String serialized = jsonb.toJson(moduleInfo);
        out.print(serialized);
    }
}
