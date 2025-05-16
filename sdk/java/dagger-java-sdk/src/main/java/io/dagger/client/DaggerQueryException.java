package io.dagger.client;

import io.smallrye.graphql.client.GraphQLError;
import java.util.Arrays;
import java.util.Collections;
import java.util.stream.Collectors;
import org.apache.commons.lang3.StringUtils;

public class DaggerQueryException extends Exception {

  private static final String SIMPLE_MESSAGE = "Message: [%s]\nPath: [%s]\nType Code: [%s]\n";
  private static final String ENHANCED_MESSAGE =
      "Message: [%s]\nPath: [%s]\nType Code: [%s]\nExit Code: [%s]\nCmd: [%s]\n";
  private static final String FULL_MESSAGE =
      "Message: [%s]\nPath: [%s]\nType Code: [%s]\nExit Code: [%s]\nCmd: [%s]\nSTDERR: [%s]\n";

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
    super(
        Arrays.stream(errors)
            .map(
                e ->
                    String.format(
                        SIMPLE_MESSAGE,
                        e.getMessage(),
                        StringUtils.join(e.getPath(), "."),
                        e.getExtensions().getOrDefault(TYPE_KEY, null)))
            .collect(Collectors.joining("\n")));
    this.errors = errors;
  }

  public GraphQLError[] getErrors() {
    return errors;
  }

  public String toEnhancedMessage() {
    return Arrays.stream(errors)
        .map(
            e ->
                String.format(
                    ENHANCED_MESSAGE,
                    e.getMessage(),
                    StringUtils.join(e.getPath(), "."),
                    e.getExtensions().getOrDefault(TYPE_KEY, null),
                    e.getExtensions().getOrDefault(EXIT_CODE_KEY, null),
                    StringUtils.join(
                        ((Object[])
                            e.getExtensions().getOrDefault(CMD_KEY, Collections.emptyList())),
                        " ")))
        .collect(Collectors.joining("\n"));
  }

  public String toFullMessage() {
    return Arrays.stream(errors)
        .map(
            e ->
                String.format(
                    FULL_MESSAGE,
                    e.getMessage(),
                    StringUtils.join(e.getPath(), "."),
                    e.getExtensions().getOrDefault(TYPE_KEY, null),
                    e.getExtensions().getOrDefault(EXIT_CODE_KEY, null),
                    StringUtils.join(
                        ((Object[])
                            e.getExtensions().getOrDefault(CMD_KEY, Collections.emptyList())),
                        " "),
                    e.getExtensions().getOrDefault(STDERR_KEY, null)))
        .collect(Collectors.joining("\n"));
  }
}
