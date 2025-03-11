package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.*;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;

@Object
public class MyModule {
  /**
   * Build an application using cached dependencies
   *
   * @param source Source code location
   */
  @Function
  public Container build(Directory source) {
    return dag()
        .container()
        .from("golang:1.12")
        .withDirectory("/src", source)
        .withWorkdir("/src")
        .withMountedCache("/go/pkg/mod", dag().cacheVolume("go-mod-121"))
        .withEnvVariable("GOMODCACHE", "/go/pkg/mod")
        .withMountedCache("/go/build-cache", dag().cacheVolume("go-build-121"))
        .withEnvVariable("GOCACHE", "/go/build-cache")
        .withExec(List.of("go", "build"));
  }
}
