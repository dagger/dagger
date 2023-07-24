package org.chelonix.dagger.client;

import io.smallrye.graphql.client.core.Field;

import java.util.concurrent.ExecutionException;

import static io.smallrye.graphql.client.core.Field.field;

class QueryPart {

    private String fieldName;
    private Arguments arguments;

    QueryPart(String fieldName) {
        this(fieldName, Arguments.noArgs());
    }

    QueryPart(String fieldName, Arguments arguments) {
        this.fieldName = fieldName;
        this.arguments = arguments;
    }

    String getOperation() {
        return fieldName;
    }

    Field toField() throws ExecutionException, InterruptedException, DaggerQueryException {
        //List<Argument> argList = arguments.entrySet().stream().map(e -> arg(e.getKey(), e.getValue().serialize())).toList();
        return field(fieldName, arguments.toList());
    }
}
