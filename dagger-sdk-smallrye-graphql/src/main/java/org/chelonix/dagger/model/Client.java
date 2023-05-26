package org.chelonix.dagger.model;

import io.smallrye.graphql.client.dynamic.api.DynamicGraphQLClient;

import java.util.concurrent.ExecutionException;

public class Client {

    private QueryContext context;

    public Client(DynamicGraphQLClient graphQLClient) {
        this.context = new QueryContext(graphQLClient);
    }

    public Container container() {
        return new Container(context.chain(new QueryPart("container")));
    }
    public Container container(ContainerID id) {
        return new Container(context.chain(new QueryPart("container", "containerID", id)));
    }
    public Container container(String platform) {
        return new Container(context.chain(new QueryPart("container", "platform", platform)));
    }
    public Container container(String id, String platform) {
        return new Container(context.chain(new QueryPart("container", "containerID", id, "platform", platform)));
    }

    public String defaultPlatform() throws ExecutionException, InterruptedException {
        return context.chain(new QueryPart("defaultPlatform")).executeQuery(String.class);
    }
}
