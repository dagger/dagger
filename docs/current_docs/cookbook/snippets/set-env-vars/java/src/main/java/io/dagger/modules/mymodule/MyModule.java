package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.*;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;
import java.util.function.UnaryOperator;

@Object
public class MyModule {
  private record EnvVar(String name, String value) {}

  /** Set environment variables in a container. */
  @Function
  public String setEnvVars() throws ExecutionException, DaggerQueryException, InterruptedException {
    return dag()
        .container()
        .from("alpine")
        .with(
            envVariables(
                List.of(
                    new EnvVar("ENV_VAR_1", "VALUE 1"),
                    new EnvVar("ENV_VAR_2", "VALUE 2"),
                    new EnvVar("ENV_VAR_3", "VALUE 3"))))
        .withExec(List.of("env"))
        .stdout();
  }

  private UnaryOperator<Container> envVariables(List<EnvVar> envs) {
    return ctr -> {
      for (EnvVar env : envs) {
        ctr = ctr.withEnvVariable(env.name(), env.value());
      }
      return ctr;
    };
  }
}
