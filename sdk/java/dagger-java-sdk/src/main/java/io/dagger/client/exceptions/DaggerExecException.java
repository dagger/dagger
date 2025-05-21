package io.dagger.client.exceptions;

import io.smallrye.graphql.client.GraphQLError;

public class DaggerExecException extends DaggerException {

  public DaggerExecException() {
    super();
  }

  public DaggerExecException(GraphQLError... errors) {
    super(DaggerExceptionUtils.toEnhancedMessage(errors), errors);
  }
}
