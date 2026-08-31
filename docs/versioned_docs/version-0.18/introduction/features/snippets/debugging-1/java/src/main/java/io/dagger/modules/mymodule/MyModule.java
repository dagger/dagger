package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  @Function
  public String foo() throws ExecutionException, DaggerQueryException, InterruptedException {
    return dag().container()
        .from("alpine:latest")
        .withExec(List.of("sh", "-c", "echo hello world > /foo"))
        .withExec(List.of("cat", "/FOO")) // deliberate error
        .stdout();
  }
}

// run with dagger call --interactive foo
