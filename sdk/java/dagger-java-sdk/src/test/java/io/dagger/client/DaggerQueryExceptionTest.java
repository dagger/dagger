package io.dagger.client;

import static io.dagger.client.exception.DaggerExceptionConstants.CMD_KEY;
import static io.dagger.client.exception.DaggerExceptionConstants.EXIT_CODE_KEY;
import static io.dagger.client.exception.DaggerExceptionConstants.STDERR_KEY;
import static io.dagger.client.exception.DaggerExceptionConstants.TYPE_EXEC_ERROR_VALUE;
import static io.dagger.client.exception.DaggerExceptionConstants.TYPE_KEY;
import static org.assertj.core.api.Assertions.assertThat;

import io.dagger.client.exception.DaggerQueryException;
import io.smallrye.graphql.client.GraphQLError;
import jakarta.json.Json;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

public class DaggerQueryExceptionTest {

  @Test
  void shouldReturnDefaultMessage() {
    GraphQLError error =
        buildError(
            "ERROR",
            new Object[] {"container", "from", "withExec", "stdout"},
            Map.of(TYPE_KEY, TYPE_EXEC_ERROR_VALUE));
    GraphQLError error2 =
        buildError(
            "ERROR2",
            new Object[] {"container", "from", "withExec", "withExec", "stdout"},
            Map.of(TYPE_KEY, TYPE_EXEC_ERROR_VALUE));

    String result = new DaggerQueryException(error, error2).getMessage();
    String expected = "ERROR\nERROR2";
    assertThat(result).isEqualTo(expected);
  }

  @Test
  void shouldReturnEnanchedMessage() {
    GraphQLError error =
        buildError(
            "ERROR",
            new Object[] {"container", "from", "withExec", "stdout"},
            Map.of(
                TYPE_KEY,
                TYPE_EXEC_ERROR_VALUE,
                EXIT_CODE_KEY,
                "1",
                CMD_KEY,
                Json.createArrayBuilder().add("cat").add("WRONG").build()));
    GraphQLError error2 =
        buildError(
            "ERROR2",
            new Object[] {"container", "from", "withExec", "withExec", "stdout"},
            Map.of(
                TYPE_KEY,
                TYPE_EXEC_ERROR_VALUE,
                EXIT_CODE_KEY,
                "2",
                CMD_KEY,
                Json.createArrayBuilder().add("cat").add("WRONG2").build()));

    String result = new DaggerQueryException(error, error2).toEnhancedMessage();
    String expected =
        "Message: [ERROR]\nPath: [container.from.withExec.stdout]\nType Code: [EXEC_ERROR]\nExit Code: [1]\nCmd: [cat WRONG]\n\nMessage: [ERROR2]\nPath: [container.from.withExec.withExec.stdout]\nType Code: [EXEC_ERROR]\nExit Code: [2]\nCmd: [cat WRONG2]\n";
    assertThat(result).isEqualTo(expected);
  }

  @Test
  void shouldReturnFullMessage() {
    GraphQLError error =
        buildError(
            "ERROR",
            new Object[] {"container", "from", "withExec", "stdout"},
            Map.of(
                TYPE_KEY,
                TYPE_EXEC_ERROR_VALUE,
                EXIT_CODE_KEY,
                "1",
                CMD_KEY,
                Json.createArrayBuilder().add("cat").add("WRONG").build(),
                STDERR_KEY,
                "DEEP ERROR DETAILS"));
    GraphQLError error2 =
        buildError(
            "ERROR2",
            new Object[] {"container", "from", "withExec", "withExec", "stdout"},
            Map.of(
                TYPE_KEY,
                TYPE_EXEC_ERROR_VALUE,
                EXIT_CODE_KEY,
                "2",
                CMD_KEY,
                Json.createArrayBuilder().add("cat").add("WRONG2").build(),
                STDERR_KEY,
                "DEEP ERROR DETAILS2"));

    String result = new DaggerQueryException(error, error2).toFullMessage();
    String expected =
        "Message: [ERROR]\nPath: [container.from.withExec.stdout]\nType Code: [EXEC_ERROR]\nExit Code: [1]\nCmd: [cat WRONG]\nSTDERR: [DEEP ERROR DETAILS]\n\nMessage: [ERROR2]\nPath: [container.from.withExec.withExec.stdout]\nType Code: [EXEC_ERROR]\nExit Code: [2]\nCmd: [cat WRONG2]\nSTDERR: [DEEP ERROR DETAILS2]\n";
    assertThat(result).isEqualTo(expected);
  }

  private GraphQLError buildError(String message, Object[] path, Map<String, Object> extensions) {
    return new GraphQLError() {
      @Override
      public String getMessage() {
        return message;
      }

      @Override
      public List<Map<String, Integer>> getLocations() {
        return null;
      }

      @Override
      public Object[] getPath() {
        return path;
      }

      @Override
      public Map<String, Object> getExtensions() {
        return extensions;
      }

      @Override
      public Map<String, Object> getOtherFields() {
        return null;
      }
    };
  }
}
