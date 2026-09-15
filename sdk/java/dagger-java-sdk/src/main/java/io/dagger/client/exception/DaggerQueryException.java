package io.dagger.client.exception;

import io.smallrye.graphql.client.GraphQLError;

public class DaggerQueryException extends Exception {

  private GraphQLError error;

  public DaggerQueryException() {
    super("An unexpected error occurred with no error details");
  }

  public DaggerQueryException(GraphQLError error) {
    super(error.getMessage());
    this.error = error;
  }

  public GraphQLError getError() {
    return error;
  }

  public String toSimpleMessage() {
    return DaggerExceptionUtils.toSimpleMessage(error);
  }

  public String toEnhancedMessage() {
    return DaggerExceptionUtils.toEnhancedMessage(error);
  }

  public String toFullMessage() {
    return DaggerExceptionUtils.toFullMessage(error);
  }
}
