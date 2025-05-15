package io.dagger.client;

import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;
import io.smallrye.graphql.client.GraphQLError;

public class DaggerQueryExceptionTest {

    @Test
    void shouldReturnDefaultMessage() {
        GraphQLError error = buildError("ERROR", new Object[]{1 , "2", "3"}, Map.of("KEY", "VALUE"));
    }

    @Test
    void shouldReturnEnanchedMessage() {
        
    }

    @Test
    void shouldReturnFullMessage() {
        
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
