package io.dagger.client;

import io.dagger.client.exception.DaggerQueryException;
import java.util.concurrent.ExecutionException;

class QueryPart {

  private String fieldName;
  private Arguments arguments;

  QueryPart(String fieldName) {
    this(fieldName, Arguments.noArgs());
  }

  QueryPart(String fieldName, Arguments arguments) {
    this.fieldName = fieldName;
    this.arguments = arguments;
  }

  String getOperation() {
    return fieldName;
  }

  String toGraphQL() throws ExecutionException, InterruptedException, DaggerQueryException {
    String args = arguments.toGraphQL();
    return args.isEmpty() ? fieldName : fieldName + "(" + args + ")";
  }
}
