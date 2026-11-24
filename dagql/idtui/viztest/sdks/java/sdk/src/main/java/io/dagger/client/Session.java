package io.dagger.client;

import io.dagger.client.engineconn.Connection;

/**
 * A connection to an engine, and the root every generated client chains its first selection from.
 *
 * <p>Hand-written because core and every module client take it, so no one client can generate it.
 */
public final class Session implements AutoCloseable {

  private final Connection connection;
  private final QueryBuilder queryBuilder;

  public Session(Connection connection) {
    this.connection = connection;
    this.queryBuilder = new QueryBuilder(connection.getGraphQLClient());
  }

  /** The root of every selection over this session. */
  public QueryBuilder queryBuilder() {
    return queryBuilder;
  }

  @Override
  public void close() throws Exception {
    connection.close();
  }
}
