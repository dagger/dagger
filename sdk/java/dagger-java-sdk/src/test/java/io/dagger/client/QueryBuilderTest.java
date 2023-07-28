package io.dagger.client;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.mock;

import io.smallrye.graphql.client.core.Document;
import io.smallrye.graphql.client.dynamic.api.DynamicGraphQLClient;
import java.util.List;
import org.junit.jupiter.api.Test;

public class QueryBuilderTest {

  @Test
  public void query_should_be_marshalled() throws Exception {
    DynamicGraphQLClient client = mock(DynamicGraphQLClient.class);
    QueryBuilder qb = new QueryBuilder(client);
    qb =
        qb.chain("core")
            .chain("image", Arguments.newBuilder().add("ref", "alpine").build())
            .chain("file", Arguments.newBuilder().add("path", "/etc/alpine-release").build());
    Document query = qb.buildDocument();
    assertThat(query.build())
        .isEqualTo("query {core{image(ref:\"alpine\"){file(path:\"/etc/alpine-release\")}}}");
  }

  @Test
  public void query_for_list_should_be_marshalled() throws Exception {
    DynamicGraphQLClient client = mock(DynamicGraphQLClient.class);
    QueryBuilder qb = new QueryBuilder(client);
    qb = qb.chain("core").chain("env", List.of("name", "value"));
    String query = qb.buildDocument().build();
    assertThat(query).isEqualTo("query {core{env{name value}}}");
  }

  @Test
  public void chaining_with_leave_is_final() throws Exception {
    DynamicGraphQLClient client = mock(DynamicGraphQLClient.class);
    QueryBuilder qb = new QueryBuilder(client);
    try {
      qb = qb.chain("core").chain("env", List.of("name", "value")).chain("failure");
    } catch (IllegalStateException iae) {
      assertThat(iae.getMessage()).isEqualTo("A new field cannot be chained");
    }
  }
}
