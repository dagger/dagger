package io.dagger.client.exception;

import static io.dagger.client.exception.DaggerExceptionConstants.TYPE_EXEC_ERROR_VALUE;
import static io.dagger.client.exception.DaggerExceptionConstants.TYPE_KEY;
import static org.assertj.core.api.Assertions.assertThat;

import io.smallrye.graphql.client.GraphQLError;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

public class DaggerExecExceptionTest {

  @Test
  void shouldReturnDefaultMessage() {
    GraphQLError error =
        buildError(
            "ERROR",
            new Object[] {"container", "from", "withExec", "stdout"},
            Map.of(TYPE_KEY, TYPE_EXEC_ERROR_VALUE));

    String result = new DaggerExecException(error).getMessage();
    String expected = "ERROR";
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
