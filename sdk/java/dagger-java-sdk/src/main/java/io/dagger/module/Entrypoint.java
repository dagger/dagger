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
                var modID = register(moduleInfo);
                result = JSON.from(modID.convert());
            } else {
                result = invoke(moduleInfo, parentJson, parentName, fnName, inputArgs);
            }
            fnCall.returnValue(result);
        } catch (Exception e) {
            fnCall.returnError(dag.error(e.getMessage()));
        }
    }

    private ModuleID register(ModuleInfo moduleInfo) throws ExecutionException, DaggerQueryException, InterruptedException {
        var module = dag
                .module();
        if (isNotBlank(moduleInfo.description())) {
            module = module.withDescription(moduleInfo.description());
        }
        for (var obj : moduleInfo.objects()) {
            var moduleObj = dag.typeDef().withObject(obj.name());
            for (var fn : obj.functions()) {
                var objFn = dag.function(fn.name(), typeDef(fn.returnType()));
                if (isNotBlank(fn.description())) {
                    objFn = objFn.withDescription(fn.description());
                }

                for (var arg : fn.parameters()) {
                    objFn = objFn.withArg(
                            arg.name(),
                            typeDef(arg.type()));
                }
                moduleObj = moduleObj.withFunction(objFn);
            }
            module = module.withObject(moduleObj);
        }

        return module.id();
    }

    private JSON invoke(ModuleInfo moduleInfo, JSON parentJson, String parentName, String fnName, Map<String, JSON> inputArgs) {
        throw new UnsupportedOperationException("Not yet implemented");
    }

    private TypeDef typeDef(String name) {
        if (name == null) {
            throw new IllegalArgumentException("Type name cannot be null");
        }
        TypeDef typeDef;
        try {
            var kind = TypeDefKind.valueOf((name + "_kind").toUpperCase());
            typeDef = dag.typeDef().withKind(kind);
        } catch (IllegalArgumentException e) {
            // FIXME: correctly handle types to match Dagger ones, for instance regarding arrays
            typeDef = dag.typeDef().withObject(name.substring(name.lastIndexOf('.') + 1));
        }

        return typeDef;
    }

    private Boolean isNotBlank(String str) {
        return str != null && !str.isBlank();
    }
}