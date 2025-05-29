package io.dagger.client.exceptions;

import static io.dagger.client.exceptions.DaggerExceptionConstants.CMD_KEY;
import static io.dagger.client.exceptions.DaggerExceptionConstants.ENHANCED_MESSAGE;
import static io.dagger.client.exceptions.DaggerExceptionConstants.EXIT_CODE_KEY;
import static io.dagger.client.exceptions.DaggerExceptionConstants.FULL_MESSAGE;
import static io.dagger.client.exceptions.DaggerExceptionConstants.SIMPLE_MESSAGE;
import static io.dagger.client.exceptions.DaggerExceptionConstants.STDERR_KEY;
import static io.dagger.client.exceptions.DaggerExceptionConstants.TYPE_KEY;

import io.smallrye.graphql.client.GraphQLError;
import jakarta.json.JsonArray;
import jakarta.json.JsonValue;
import java.util.Arrays;
import java.util.stream.Collectors;
import org.apache.commons.lang3.StringUtils;

public class DaggerExceptionUtils {

  private DaggerExceptionUtils() {}

  public static Object getExtensionValueByKey(GraphQLError error, String key) {
    if (error == null || StringUtils.isBlank(key) || error.getExtensions() == null) {
      return null;
    }

    return error.getExtensions().getOrDefault(key, null);
  }

  public static String getPath(GraphQLError error) {
    return StringUtils.join(error.getPath(), ".");
  }

  public static String getCmd(GraphQLError error) {
    Object cmdList = getExtensionValueByKey(error, CMD_KEY);
    String cmd = "";
    if (cmdList != null && cmdList instanceof JsonArray array) {
      cmd =
          array.stream()
              .map(JsonValue::toString)
              .collect(Collectors.joining(" "))
              .replace("\"", "");
    }
    return cmd;
  }

  public static String getType(GraphQLError error) {
    return (String) getExtensionValueByKey(error, TYPE_KEY);
  }

  public static String getExitCode(GraphQLError error) {
    return (String) getExtensionValueByKey(error, EXIT_CODE_KEY);
  }

  public static String getStdErr(GraphQLError error) {
    return (String) getExtensionValueByKey(error, STDERR_KEY);
  }

  public static String toSimpleMessage(GraphQLError... errors) {
    return Arrays.stream(errors)
        .map(e -> String.format(SIMPLE_MESSAGE, e.getMessage(), getPath(e), getType(e)))
        .collect(Collectors.joining("\n"));
  }

  public static String toEnhancedMessage(GraphQLError... errors) {
    return Arrays.stream(errors)
        .map(
            e ->
                String.format(
                    ENHANCED_MESSAGE,
                    e.getMessage(),
                    getPath(e),
                    getType(e),
                    getExitCode(e),
                    getCmd(e)))
        .collect(Collectors.joining("\n"));
  }

  public static String toFullMessage(GraphQLError... errors) {
    return Arrays.stream(errors)
        .map(
            e ->
                String.format(
                    FULL_MESSAGE,
                    e.getMessage(),
                    getPath(e),
                    getType(e),
                    getExitCode(e),
                    getCmd(e),
                    getExtensionValueByKey(e, STDERR_KEY)))
        .collect(Collectors.joining("\n"));
  }
}
