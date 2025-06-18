package io.dagger.client.exception;

import static io.dagger.client.exception.DaggerExceptionConstants.CMD_KEY;
import static io.dagger.client.exception.DaggerExceptionConstants.ENHANCED_MESSAGE;
import static io.dagger.client.exception.DaggerExceptionConstants.EXIT_CODE_KEY;
import static io.dagger.client.exception.DaggerExceptionConstants.FULL_MESSAGE;
import static io.dagger.client.exception.DaggerExceptionConstants.SIMPLE_MESSAGE;
import static io.dagger.client.exception.DaggerExceptionConstants.STDERR_KEY;
import static io.dagger.client.exception.DaggerExceptionConstants.STDOUT_KEY;
import static io.dagger.client.exception.DaggerExceptionConstants.TYPE_KEY;

import io.smallrye.graphql.client.GraphQLError;
import jakarta.json.JsonArray;
import jakarta.json.JsonValue;
import java.util.Arrays;
import java.util.List;
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

  public static List<String> getPath(GraphQLError error) {
    return Arrays.stream(error.getPath()).map(Object::toString).toList();
  }

  public static List<String> getCmd(GraphQLError error) {
    Object cmdList = getExtensionValueByKey(error, CMD_KEY);
    if (cmdList instanceof JsonArray array) {
      return array.getValuesAs(JsonString.class).stream().map(JsonString::getString).toList();
    }
    return null;
  }

  public static String getType(GraphQLError error) {
    return String.valueOf(getExtensionValueByKey(error, TYPE_KEY));
  }

  public static Integer getExitCode(GraphQLError error) {
    return Integer.valueOf(String.valueOf(getExtensionValueByKey(error, EXIT_CODE_KEY)));
  }

  public static String getStdOut(GraphQLError error) {
    return String.valueOf(getExtensionValueByKey(error, STDOUT_KEY));
  }

  public static String getStdErr(GraphQLError error) {
    return String.valueOf(getExtensionValueByKey(error, STDERR_KEY));
  }

  public static String toSimpleMessage(GraphQLError... errors) {
    return Arrays.stream(errors)
        .map(
            e ->
                String.format(
                    SIMPLE_MESSAGE, e.getMessage(), StringUtils.join(getPath(e), "."), getType(e)))
        .collect(Collectors.joining("\n"));
  }

  public static String toEnhancedMessage(GraphQLError... errors) {
    return Arrays.stream(errors)
        .map(
            e ->
                String.format(
                    ENHANCED_MESSAGE,
                    e.getMessage(),
                    StringUtils.join(getPath(e), "."),
                    getType(e),
                    getExitCode(e),
                    StringUtils.join(getCmd(e), ",")))
        .collect(Collectors.joining("\n"));
  }

  public static String toFullMessage(GraphQLError... errors) {
    return Arrays.stream(errors)
        .map(
            e ->
                String.format(
                    FULL_MESSAGE,
                    e.getMessage(),
                    StringUtils.join(getPath(e), "."),
                    getType(e),
                    getExitCode(e),
                    StringUtils.join(getCmd(e), ","),
                    getExtensionValueByKey(e, STDERR_KEY)))
        .collect(Collectors.joining("\n"));
  }
}
