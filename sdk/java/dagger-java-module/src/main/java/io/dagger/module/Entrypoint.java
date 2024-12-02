package io.dagger.module;

import io.dagger.client.Client;
import io.dagger.client.Dagger;
import io.dagger.client.FunctionCall;

public class Entrypoint {
    public static void main(String... args) throws Exception {
        try (Client dag = Dagger.connect()) {
            new Entrypoint(dag).handle();
        }
    }

    private final Client dag;

    Entrypoint(Client dag) {
        this.dag = dag;
    }

    void handle() throws Exception {
        FunctionCall fnCall = dag.currentFunctionCall();
        String moduleName = dag.currentModule().name();
        System.out.println("Module Name: " + moduleName);
        String parentName = fnCall.parentName();
        System.out.println("Parent Name: " + parentName);
        if (parentName.isEmpty()) {
            // register
            System.out.println("register");
            // ModuleID modId = register(moduleName);
        } else {
            // invoke
            System.out.println("invoke");
        }
    }

    /*
    private ModuleID register(String moduleName) throws InterruptedException, ExecutionException, DaggerQueryException {
        Module mod = dag.module();
        mod = mod.withDescription("A new Dagger module in java");

        ClassLoader classLoader = loadJar("FIXME");

        Set<Class<?>> annotatedClasses = reflections.getTypesAnnotatedWith(VotreAnnotation.class);
        return mod.id();
    }
         */

         /*
    private ClassLoader loadJar(String path) {
        File jarFile = new File(path);
        URL jarUrl = jarFile.toURI().toURL();
        return new URLClassLoader(new URL[]{jarUrl}, ClassLoader.getSystemClassLoader());
    }
        */
}