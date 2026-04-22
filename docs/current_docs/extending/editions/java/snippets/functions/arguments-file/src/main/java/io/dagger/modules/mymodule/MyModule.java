package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.exception.DaggerQueryException;
import io.dagger.client.File;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  @Function
  public String readFile(File source)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    return dag().container()
        .from("alpine:latest")
        .withFile("/src/myfile", source)
        .withExec(List.of("cat", "/src/myfile"))
        .stdout();
  }
}
