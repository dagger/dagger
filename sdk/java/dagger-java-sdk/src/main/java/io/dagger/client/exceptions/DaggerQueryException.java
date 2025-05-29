package io.dagger.client.exceptions;

import io.smallrye.graphql.client.GraphQLError;

public class DaggerQueryException extends DaggerException {

  public DaggerQueryException() {
    super("An unexpected error occurred with no error details");
  }

  public DaggerQueryException(GraphQLError... errors) {
    super(DaggerExceptionUtils.toSimpleMessage(errors), errors);
  }
}
