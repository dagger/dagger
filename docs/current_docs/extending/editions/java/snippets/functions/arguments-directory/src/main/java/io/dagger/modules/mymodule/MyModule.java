package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;
import io.dagger.client.exception.DaggerQueryException;


import io.dagger.client.Directory;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  @Function
  public String tree(Directory src, String depth)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    return dag().container()
        .from("alpine:latest")
        .withMountedDirectory("/mnt", src)
        .withWorkdir("/mnt")
        .withExec(List.of("apk", "add", "tree"))
        .withExec(List.of("tree", "-L", depth))
        .stdout();
  }
}
