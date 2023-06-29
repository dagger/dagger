package org.chelonix.dagger.client;

import io.smallrye.graphql.client.dynamic.api.DynamicGraphQLClient;
import io.smallrye.graphql.client.dynamic.api.DynamicGraphQLClientBuilder;
import org.chelonix.dagger.client.engineconn.Connection;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.util.Base64;

public class Dagger {

    public static Client connect() throws IOException {
        return connect(System.getProperty("user.dir"));
    }

    public static Client connect(String workingDir) throws IOException {
        return new Client(Connection.get(workingDir));
    }
}
