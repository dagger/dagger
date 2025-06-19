package io.dagger.client.exception;

import io.smallrye.graphql.client.GraphQLError;
import java.util.List;

public class DaggerExecException extends DaggerQueryException {

  public DaggerExecException() {
    super();
  }

  public DaggerExecException(GraphQLError error) {
    super(error);
  }

  public Integer getExitCode() {
    return DaggerExceptionUtils.getExitCode(getError());
  }

  public List<String> getPath() {
    return DaggerExceptionUtils.getPath(getError());
  }

  public List<String> getCmd() {
    return DaggerExceptionUtils.getCmd(getError());
  }

  public String getStdOut() {
    return DaggerExceptionUtils.getStdOut(getError());
  }

  public String getStdErr() {
    return DaggerExceptionUtils.getStdErr(getError());
  }
}
