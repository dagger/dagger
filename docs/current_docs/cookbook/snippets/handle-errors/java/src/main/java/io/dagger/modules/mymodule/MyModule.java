package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.exception.DaggerExecException;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  /** Generate an error */
  @Function
  public String test() throws ExecutionException, DaggerQueryException, InterruptedException {
    try {
      return dag()
          .container()
          .from("alpine")
          // ERROR: cat: read error: Is a directory
          .withExec(List.of("cat", "/"))
          .stdout();
    } catch (DaggerExecException dee) {
      return "Test pipeline failure: %s".formatted(dee.getStdErr());
    }
  }
}
