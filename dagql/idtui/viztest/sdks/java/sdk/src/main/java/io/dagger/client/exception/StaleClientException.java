package io.dagger.client.exception;

import io.dagger.client.ModuleTarget;
import io.dagger.client.graphql.GraphQLError;
import java.util.Collection;
import java.util.stream.Collectors;

/**
 * A generated client's selection does not validate against the module the engine serves: the client
 * was generated against another version of its target. Regenerate it with {@code dagger generate}.
 */
public class StaleClientException extends DaggerQueryException {

  public StaleClientException(GraphQLError error, Collection<ModuleTarget> targets) {
    super(
        error.getMessage()
            + (targets.size() == 1 ? " The client of " : " The clients of ")
            + targets.stream().map(StaleClientException::describe).collect(Collectors.joining(", "))
            + (targets.size() == 1 ? " is" : " are")
            + " out of date. Run `dagger generate`.",
        error);
  }

  private static String describe(ModuleTarget target) {
    return target.pin() == null ? target.address() : target.address() + " at " + target.pin();
  }
}
