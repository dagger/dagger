package io.dagger.client.exception;

import io.dagger.client.graphql.GraphQLError;

public class DaggerQueryException extends Exception {

  private GraphQLError error;

  public DaggerQueryException() {
    super("An unexpected error occurred with no error details");
  }

  public DaggerQueryException(GraphQLError error) {
    this(error.getMessage(), error);
  }

  protected DaggerQueryException(String message, GraphQLError error) {
    super(message);
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
