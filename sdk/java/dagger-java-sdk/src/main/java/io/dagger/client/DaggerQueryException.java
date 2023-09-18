package io.dagger.client;

import io.smallrye.graphql.client.GraphQLError;
import java.util.Arrays;
import java.util.stream.Collectors;

public class DaggerQueryException extends Exception {

  private GraphQLError[] errors;

  public DaggerQueryException() {
    super("An unexpected error occured with no error details");
    this.errors = new GraphQLError[0];
  }

  public DaggerQueryException(GraphQLError... errors) {
    super(Arrays.stream(errors).map(GraphQLError::getMessage).collect(Collectors.joining("\n")));
    this.errors = errors;
  }

  public GraphQLError[] getErrors() {
    return errors;
  }
}
