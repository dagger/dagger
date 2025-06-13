package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.Directory;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;

@Object
public class MyModule {
  /**
   * Return a container with a specified directory and an additional file
   * @param source Source directory
   */
  @Function
  public Container copyAndModifyDirectory(Directory source) {
    return dag().container()
        .from("alpine:latest")
        .withDirectory("/src", source)
        .withExec(List.of("/bin/sh", "-c", "echo foo > /src/foo"));
  }
}
