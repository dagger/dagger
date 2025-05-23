package io.dagger.client.exceptions;

import io.smallrye.graphql.client.GraphQLError;

public abstract class DaggerException extends Exception {

  private GraphQLError[] errors;

  public DaggerException() {
    super("An unexpected error occurred with no error details");
    this.errors = new GraphQLError[0];
  }

  public DaggerException(String message, GraphQLError... errors) {
    super(message);
    this.errors = errors;
  }

  public GraphQLError[] getErrors() {
    return errors;
  }

  public String toSimpleMessage() {
    return DaggerExceptionUtils.toSimpleMessage(errors);
  }

  public String toEnhancedMessage() {
    return DaggerExceptionUtils.toEnhancedMessage(errors);
  }

  public String toFullMessage() {
    return DaggerExceptionUtils.toFullMessage(errors);
  }
}
