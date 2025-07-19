package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.*;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  /** Set a single environment variable in a container. */
  @Function
  public String setEnvVar() throws ExecutionException, DaggerQueryException, InterruptedException {
    return dag()
        .container()
        .from("alpine")
        .withEnvVariable("ENV_VAR", "VALUE")
        .withExec(List.of("env"))
        .stdout();
  }
}
