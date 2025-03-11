package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.*;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  /**
   * Return a container with a mounted file
   *
   * @param f Source file
   */
  @Function
  public Container mountFile(File f)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    String name = f.name();
    return dag().container().from("alpine:latest").withMountedFile("/src/%s".formatted(name), f);
  }
}
