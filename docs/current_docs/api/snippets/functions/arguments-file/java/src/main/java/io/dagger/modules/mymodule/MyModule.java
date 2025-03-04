package io.dagger.modules.mymodule;

import io.dagger.client.DaggerQueryException;
import io.dagger.client.File;
import io.dagger.module.AbstractModule;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;

import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule extends AbstractModule {
  @Function
  public String readFile(File source)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    return dag.container()
        .from("alpine:latest")
        .withFile("/src/myfile", source)
        .withExec(List.of("cat", "/src/myfile"))
        .stdout();
  }
}
