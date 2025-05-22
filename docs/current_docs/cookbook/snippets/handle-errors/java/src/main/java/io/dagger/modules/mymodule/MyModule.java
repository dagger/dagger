package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.*;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  /** Generate an error */
  @Function
  public String test() {
    try {
      String out =
          dag()
              .container()
              .from("alpine")
              // ERROR: cat: read error: Is a directory
              .withExec(List.of("cat", "/"))
              .stdout();
      return out;
    } catch (DaggerQueryException e) {
      return "Test pipeline failure: %s".formatted(e.getMessage());
    } catch (InterruptedException | ExecutionException e) {
      throw new RuntimeException(e);
    }
  }
}
