package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.Directory;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;

@Object
public class MyModule {
  /**
   * Return a container with a mounted directory
   *
   * @param source Source directory
   */
  @Function
  public Container mountDirectory(Directory source) {
    return dag()
        .container()
        .from("alpine:latest")
        .withMountedDirectory("/src", source);
  }
}
