package io.dagger.module;

import com.google.gson.Gson;
import io.dagger.client.*;
import io.dagger.module.info.ModuleInfo;

import java.io.BufferedReader;
import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.util.HashMap;
import java.util.Map;
import java.util.concurrent.ExecutionException;

public class Entrypoint extends Base {
    public static void main(String... args) throws Exception {
        try (Client dag = Dagger.connect()) {
            new Entrypoint(dag).dispatch();
        }
    }

    Entrypoint(Client dag) {
        super(dag);
    }

    void dispatch() throws IOException, ExecutionException, DaggerQueryException, InterruptedException {
        var fnCall = dag.currentFunctionCall();
        try {
            String moduleName = dag.currentModule().name();
            var parentName = fnCall.parentName();
            var fnName = fnCall.name();
            var parentJson = fnCall.parent();
            var fnArgs = fnCall.inputArgs();

            Map<String, JSON> inputArgs = new HashMap<>();
            for (var fnArg : fnArgs) {
                inputArgs.put(fnArg.name(), fnArg.value());
            }

            ModuleInfo moduleInfo;
            ClassLoader classloader = Thread.currentThread().getContextClassLoader();
            try (InputStream is = classloader.getResourceAsStream("dagger_module_info.json")) {
                if (is == null) {
                    throw new IOException("dagger_module_info.json not found");
                }
                BufferedReader reader = new BufferedReader(new InputStreamReader(is));
                Gson gson = new Gson();
                moduleInfo = gson.fromJson(reader, ModuleInfo.class);
            }

            JSON result;
            if (parentName.isEmpty()) {
                result = register(moduleInfo);
            } else {
                result = invoke(moduleInfo, parentJson, parentName, fnName, inputArgs);
            }
            fnCall.returnValue(result);
        } catch (Exception e) {
            fnCall.returnError(dag.error(e.getMessage()));
        }
    }

    private JSON register(ModuleInfo moduleInfo) {
        System.out.println("invoke module " + moduleInfo.name());
        var module = dag
                .module()
                .withDescription(moduleInfo.description());
        for (var obj : moduleInfo.objects()) {
            System.out.println("object " + obj.name());
            var moduleObj = dag.typeDef().withObject(obj.name());
            for (var fn : obj.functions()) {
                System.out.println("function " + fn.name());
                var objFn = dag.function(fn.name(), typeDef(fn.returnType())).withDescription(fn.description());

                for (var arg : fn.parameters()) {
                    objFn = objFn.withArg(
                            arg.name(),
                            typeDef(arg.type()));
                }
                moduleObj = moduleObj.withFunction(objFn);
            }
            module = module.withObject(moduleObj);
        }

        System.out.println(module);

        Jsonb jsonb = JsonbBuilder.create();
        String serialized = jsonb.toJson(module);
        System.out.println(serialized);
        return JSON.from(serialized);
    }

    private JSON invoke(ModuleInfo moduleInfo, JSON parentJson, String parentName, String fnName, Map<String, JSON> inputArgs) {
        throw new UnsupportedOperationException("Not yet implemented");
    }

    private TypeDef typeDef(String name) {
        TypeDef typeDef;
        try {
            var kind = TypeDefKind.valueOf((name + "_kind").toUpperCase());
            typeDef = dag.typeDef().withKind(kind);
        } catch (IllegalArgumentException e) {
            typeDef = dag.typeDef().withObject(name);
        }

        return typeDef;
    }
}