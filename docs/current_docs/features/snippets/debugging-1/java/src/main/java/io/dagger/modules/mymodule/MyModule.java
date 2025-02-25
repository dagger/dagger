package io.dagger.modules.mymodule;

import io.dagger.client.DaggerQueryException;
import io.dagger.module.AbstractModule;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule extends AbstractModule {
  @Function
  public String foo() throws ExecutionException, DaggerQueryException, InterruptedException {
    return dag.container()
        .from("alpine:latest")
        .withExec(List.of("sh", "-c", "echo hello world > /foo"))
        .withExec(List.of("cat", "/FOO")) // deliberate error
        .stdout();
  }
}

// run with dagger call --interactive foo
