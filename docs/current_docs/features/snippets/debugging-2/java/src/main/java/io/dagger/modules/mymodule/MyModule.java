package io.dagger.modules.mymodule;

import io.dagger.client.Container;
import io.dagger.module.AbstractModule;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;

@Object
public class MyModule extends AbstractModule {
  @Function
  public Container foo() {
    return dag.container()
        .from("alpine:latest")
        .terminal()
        .withExec(List.of("sh", "-c", "echo hello world > /foo"))
        .terminal();
  }
}
