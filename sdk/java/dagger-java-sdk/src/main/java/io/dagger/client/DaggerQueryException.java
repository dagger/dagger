package io.dagger.client;

import java.util.Arrays;
import java.util.Collections;
import java.util.stream.Collectors;
import io.smallrye.graphql.client.GraphQLError;

public class DaggerQueryException extends Exception {

  private static final String ENANCHED_MESSAGE =
      "Message: [%s]\nPath: [%s]\\Type Code: [%s]\nExit Code: [%s]\\Cmd: [%s]\n";
  private static final String FULL_MESSAGE =
      "Message: [%s]\nPath: [%s]\\Type Code: [%s]\nExit Code: [%s]\\Cmd: [%s]\\STDERR: [%s] \n";

  protected static final String TYPE_KEY = "_type";
  protected static final String EXIT_CODE_KEY = "exitCode";
  protected static final String CMD_KEY = "cmd";
  protected static final String STDERR_KEY = "stderr";

  private GraphQLError[] errors;

  public DaggerQueryException() {
    super("An unexpected error occurred with no error details");
    this.errors = new GraphQLError[0];
  }

  public DaggerQueryException(GraphQLError... errors) {
    super(Arrays.stream(errors).map(GraphQLError::getMessage).collect(Collectors.joining("\n")));
    this.errors = errors;
  }

  public GraphQLError[] getErrors() {
    return errors;
  }

  public String toEnanchedMessage() {
    return Arrays.stream(errors)
        .map(e -> String.format(ENANCHED_MESSAGE, e.getMessage(),
            Arrays.stream(e.getPath()).reduce((a, b) -> ((String) a) + ((String) b)),
            e.getExtensions().getOrDefault(TYPE_KEY, null),
            e.getExtensions().getOrDefault(EXIT_CODE_KEY, null),
            Arrays.stream(
                (Object[]) e.getExtensions().getOrDefault(EXIT_CODE_KEY, Collections.emptyList()))
                .reduce((a, b) -> ((String) a) + ((String) b))))
        .collect(Collectors.joining("\n"));
  }

  public String toFullMessage() {
    return Arrays.stream(errors)
        .map(e -> String.format(FULL_MESSAGE, e.getMessage(),
            Arrays.stream(e.getPath()).reduce((a, b) -> ((String) a) + ((String) b)),
            e.getExtensions().getOrDefault(TYPE_KEY, null),
            e.getExtensions().getOrDefault(EXIT_CODE_KEY, null),
            Arrays.stream(
                (Object[]) e.getExtensions().getOrDefault(CMD_KEY, Collections.emptyList()))
                .reduce((a, b) -> ((String) a) + ((String) b)),
            e.getExtensions().getOrDefault(STDERR_KEY, null)))
        .collect(Collectors.joining("\n"));
  }
}
