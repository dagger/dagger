package org.chelonix.dagger.model;

import com.jayway.jsonpath.JsonPath;
import io.smallrye.graphql.client.Response;
import io.smallrye.graphql.client.core.Document;
import io.smallrye.graphql.client.core.Field;
import io.smallrye.graphql.client.dynamic.api.DynamicGraphQLClient;
import jakarta.json.JsonArray;
import jakarta.json.JsonObject;
import jakarta.json.bind.Jsonb;
import jakarta.json.bind.JsonbBuilder;
import jakarta.json.bind.JsonbConfig;
import org.apache.commons.lang3.reflect.TypeUtils;

import java.util.*;
import java.util.concurrent.ExecutionException;
import java.util.stream.Collectors;

import static io.smallrye.graphql.client.core.Document.document;
import static io.smallrye.graphql.client.core.Operation.operation;

public class QueryContext {

    private final DynamicGraphQLClient client;
    private final Deque<QueryPart> parts;
    private List<String> finalOperations;

    public QueryContext(DynamicGraphQLClient client) {
        this(client, new LinkedList<>(), new ArrayList<>());
    }

    private QueryContext(DynamicGraphQLClient client, Deque<QueryPart> parts, List<String> finalOperations) {
        this.client = client;
        this.parts = parts;
        this.finalOperations = finalOperations;
    }

    public QueryContext chain(QueryPart part) {
        Deque<QueryPart> list = new LinkedList<>();
        list.addAll(this.parts);
        list.push(part);
        return new QueryContext(client, list, finalOperations);
    }

    public QueryContext chain(QueryPart part, List<String> finalParts) {
        Deque<QueryPart> list = new LinkedList<>();
        list.addAll(this.parts);
        list.push(part);
        List<String> list2 = new ArrayList<>();
        list2.addAll(finalParts);
        return new QueryContext(client, list, list2);
    }

    <T> T executeQuery(Class<T> klass) throws ExecutionException, InterruptedException {
        String path = parts.stream().map(QueryPart::getOperation).collect(Collectors.collectingAndThen(Collectors.toList(), list -> {
            Collections.reverse(list);
            return String.join(".", list);
        }));
        System.out.println(path);
        QueryPart terminal = parts.pop();
        Field operation = parts.stream().map(QueryPart::toField).reduce(terminal.toField(), (acc, field) -> {
            field.setFields(List.of(acc));
            return field;
        });
        Document query = document(operation(operation));
        // System.out.println(query.build());
        Response response = client.executeSync(query);
        return JsonPath.parse(response.getData().toString()).read(path, klass);
    }

    <T> List<T> executeListQuery(Class<T> klass) throws ExecutionException, InterruptedException {
        List<String> pathElts = parts.stream().map(QueryPart::getOperation).collect(Collectors.collectingAndThen(Collectors.toList(), list -> {
            Collections.reverse(list);
            return list;
        }));

        System.out.println(pathElts);
        QueryPart terminal = parts.pop();
        Field operation = parts.stream().map(QueryPart::toField).reduce(terminal.toField(finalOperations), (acc, field) -> {
            field.setFields(List.of(acc));
            return field;
        });
        Document query = document(operation(operation));
        System.out.println(query.build());
        Response response = client.executeSync(query);
        System.out.println(response);

        JsonObject obj = response.getData();
        for (int i = 0; i < pathElts.size() - 1; i++) {
            obj = obj.getJsonObject(pathElts.get(i));
        }
        JsonArray array = obj.getJsonArray(pathElts.get(pathElts.size() - 1));
        System.out.println(array);
        JsonbConfig config = new JsonbConfig().withPropertyVisibilityStrategy(new PrivateVisibilityStrategy());
        Jsonb jsonb = JsonbBuilder.newBuilder().withConfig(config).build();
        List<T> rv = jsonb.fromJson(array.toString(), TypeUtils.parameterize(List.class, klass));
        return rv;
    }
}
