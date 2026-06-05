package io.dagger.client;

import io.dagger.client.engineconn.Connection;

public class AutoCloseableClient extends Client implements AutoCloseable {
  AutoCloseableClient(Connection connection) {
    super(connection);
  }

  AutoCloseableClient(QueryBuilder queryBuilder) {
    super(queryBuilder);
  }
}
