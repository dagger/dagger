package org.chelonix.dagger;

import io.smallrye.graphql.client.Response;
import io.smallrye.graphql.client.core.Document;
import io.smallrye.graphql.client.dynamic.api.DynamicGraphQLClient;
import io.smallrye.graphql.client.dynamic.api.DynamicGraphQLClientBuilder;
import org.chelonix.dagger.model.Client;
import org.chelonix.dagger.model.Container;
import org.chelonix.dagger.model.EnvVariable;

import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import java.util.Base64;
import java.util.List;

import static io.smallrye.graphql.client.core.Argument.arg;
import static io.smallrye.graphql.client.core.Argument.args;
import static io.smallrye.graphql.client.core.Document.document;
import static io.smallrye.graphql.client.core.Field.field;
import static io.smallrye.graphql.client.core.Operation.operation;

public class Main {
    public static void main(String[] args) throws Exception {
        int port = Integer.parseInt(System.getenv("DAGGER_SESSION_PORT"));
        // int port = 0;
        String token = System.getenv("DAGGER_SESSION_TOKEN");
        System.out.println(token);
        System.out.println(System.getProperty("os.name"));
        System.out.println(System.getProperty("os.arch"));

        String encodedToken = Base64.getEncoder().encodeToString((token + ":").getBytes(StandardCharsets.UTF_8));
        DynamicGraphQLClient dynamicGraphQLClient = DynamicGraphQLClientBuilder.newBuilder().url(String.format("http://127.0.0.1:%d/query", port))
                .header("authorization", "Basic " + encodedToken).build();

//        String query = """
//            query {
//              container {
//                from (address: "alpine:latest") {
//                  withExec(args:["uname", "-nrio"]) {
//                    stdout
//                  }
//                }
//              }
//            }""";

        Document query = document(
                operation(
                        field("container",
                                field("from", args(arg("address", "alpine:latest")),
                                        field("envVariables",
                                            field("name"), field("value")
                                        )
                                )
                        )
                )
        );

        System.out.println(query.build());
        Response r = dynamicGraphQLClient.executeSync(query.build());
        System.out.println(r);

        Client client = new Client(dynamicGraphQLClient);

        String stdout = client.container().from("alpine:latest").withExec(List.of("uname", "-nrio")).stdout();
        System.out.println(stdout);

        String defaultPlatform = client.defaultPlatform();
        System.out.println(defaultPlatform);


        Container container = client.container()
                .from("alpine")
                .withExec("apk", "add", "curl")
                .withExec("curl", "https://example.com");

        // String result = container.stdout();

        // System.out.println(result);

        List<EnvVariable> env = client.container().from("alpine").envVariables();
        env.stream().map(var -> String.format("%s=%s", var.name(), var.value())).forEach(System.out::println);

        System.exit(0);
    }
}
