package io.dagger.client;

import static io.dagger.client.exceptions.DaggerExceptionConstants.CMD_KEY;
import static io.dagger.client.exceptions.DaggerExceptionConstants.EXIT_CODE_KEY;
import static io.dagger.client.exceptions.DaggerExceptionConstants.TYPE_EXEC_ERROR_VALUE;
import static io.dagger.client.exceptions.DaggerExceptionConstants.TYPE_KEY;
import static org.assertj.core.api.Assertions.assertThat;

import io.dagger.client.exceptions.DaggerExecException;
import io.smallrye.graphql.client.GraphQLError;
import jakarta.json.Json;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

public class DaggerExecExceptionTest {

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

    String result = new DaggerExecException(error, error2).getMessage();
    String expected =
        "Message: [ERROR]\nPath: [container.from.withExec.stdout]\nType Code: [EXEC_ERROR]\nExit Code: [1]\nCmd: [cat WRONG]\n\nMessage: [ERROR2]\nPath: [container.from.withExec.withExec.stdout]\nType Code: [EXEC_ERROR]\nExit Code: [2]\nCmd: [cat WRONG2]\n";
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
