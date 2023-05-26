package org.chelonix.dagger.model;

import java.util.Arrays;
import java.util.List;
import java.util.concurrent.ExecutionException;

public class Container {

    private QueryContext context;

    Container(QueryContext context) {
        this.context = context;
    }

    public Container from(String address) {
        return new Container(context.chain(new QueryPart("from", "address", address)));
    }

    public Container withExec(List<String> args) {
        return new Container(context.chain(new QueryPart("withExec", "args", args)));
    }

    public Container withExec(String ...args) {
        return new Container(context.chain(new QueryPart("withExec", "args", Arrays.asList(args))));
    }

    public String stdout() throws ExecutionException, InterruptedException {
        return context.chain(new QueryPart("stdout")).executeQuery(String.class);
    }

    public List<EnvVariable> envVariables() throws Exception {
        QueryContext ctx = context.chain(new QueryPart("envVariables"), List.of("name", "value"));
        return ctx.executeListQuery(EnvVariable.class);
    }
}
