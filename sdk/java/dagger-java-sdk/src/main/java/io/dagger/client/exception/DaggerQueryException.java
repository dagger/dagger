package io.dagger.client.exception;

import io.smallrye.graphql.client.GraphQLError;
import java.util.Arrays;
import java.util.stream.Collectors;

public class DaggerQueryException extends Exception {

  private GraphQLError[] errors;

  public DaggerQueryException() {
    super("An unexpected error occurred with no error details");
  }

  public DaggerQueryException(GraphQLError... errors) {
    super(Arrays.stream(errors).map(GraphQLError::getMessage).collect(Collectors.joining("\n")));
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
