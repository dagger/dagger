package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.*;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;

@Object
public class MyModule {
  /** Return a container */
  @Function
  public Container base() {
    return dag()
        .container()
        .from("alpine:latest")
        .withExec(List.of("mkdir", "/src"))
        .withExec(List.of("touch", "/src/foo", "/src/bar"));
  }
}
