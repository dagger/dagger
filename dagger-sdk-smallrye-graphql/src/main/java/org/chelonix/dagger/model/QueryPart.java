package org.chelonix.dagger.model;

import io.smallrye.graphql.client.core.Argument;
import io.smallrye.graphql.client.core.Field;

import java.util.HashMap;
import java.util.List;
import java.util.Map;

import static io.smallrye.graphql.client.core.Argument.arg;
import static io.smallrye.graphql.client.core.Field.field;

class QueryPart {
    private String operation;
    private Map<String, Object> arguments;

    QueryPart(String operation, Map<String, Object> arguments) {
        this.operation = operation;
        this.arguments = arguments;
    }

    QueryPart(String operation, String argName, Object argValue) {
        this.operation = operation;
        this.arguments = new HashMap<>() {{
            put(argName, argValue);
        }};
    }

    QueryPart(String operation, String argName1, Object argValue1, String argName2, Object argValue2) {
        this.operation = operation;
        this.arguments = new HashMap<>() {{
            put(argName1, argValue1);
            put(argName2, argValue2);
        }};
    }

    QueryPart(String operation) {
        this.operation = operation;
        this.arguments = arguments = new HashMap<>();
    }

    public String getOperation() {
        return operation;
    }

    Field toField() {
        List<Argument> argList = arguments.entrySet().stream().map(e -> arg(e.getKey(), e.getValue() instanceof Scalar ? ((Scalar<?>) e.getValue()).convert() : e.getValue())).toList();
        return field(operation, argList);
    }

    Field toField(List<String> subops) {
        List<Argument> argList = arguments.entrySet().stream().map(e -> arg(e.getKey(), e.getValue() instanceof Scalar ? ((Scalar<?>) e.getValue()).convert() : e.getValue())).toList();
        Field[] fields = subops.stream().map(s -> field(s)).toArray(len -> new Field[len]);
        return field(operation, argList, fields);
    }
}
