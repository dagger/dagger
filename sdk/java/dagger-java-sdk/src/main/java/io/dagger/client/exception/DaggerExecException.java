package io.dagger.client.exception;

import io.smallrye.graphql.client.GraphQLError;
import java.util.Arrays;
import java.util.List;
import java.util.Objects;
import java.util.stream.Collectors;

public class DaggerExecException extends DaggerQueryException {

  public DaggerExecException() {
    super();
  }

  public DaggerExecException(GraphQLError... errors) {
    super(errors);
  }

  public List<String> getExitCode() {
    return Arrays.asList(getErrors()).stream()
        .map(DaggerExceptionUtils::getExitCode)
        .filter(Objects::nonNull)
        .toList();
  }

  public List<String> getPath() {
    return Arrays.asList(getErrors()).stream()
        .map(DaggerExceptionUtils::getPath)
        .filter(Objects::nonNull)
        .toList();
  }

  public List<String> getCmd() {
    return Arrays.asList(getErrors()).stream()
        .map(DaggerExceptionUtils::getCmd)
        .filter(Objects::nonNull)
        .toList();
  }

  public String getStdOut() {
    return Arrays.asList(getErrors()).stream()
        .map(DaggerExceptionUtils::getStdOut)
        .collect(Collectors.joining(" "))
        .replace("\"", "");
  }

  public String getStdErr() {
    return Arrays.asList(getErrors()).stream()
        .map(DaggerExceptionUtils::getStdErr)
        .collect(Collectors.joining(" "))
        .replace("\"", "");
  }
}
