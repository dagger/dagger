package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.*;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {

  private Container builder;

  /**
   * Build and publish Docker container
   * @param src source code location. Can be local directory or remote Git repository
   */
  @Function
  public String build(Directory src) throws ExecutionException, DaggerQueryException, InterruptedException {
    // build app
    builder = dag().container()
        .from("golang:latest")
        .withDirectory("/src", src)
        .withWorkdir("/src")
        .withEnvVariable("CGO_ENABLED", "0")
        .withExec(List.of("go", "build", "-o", "myapp"));

    // publish binary on alpine base
    Container prodImage = dag().container()
        .from("alpine")
        .withFile("/bin/myapp", builder.file("/src/myapp"))
        .withEntrypoint(List.of("/bin/myapp"));

    // publish to ttl.sh registry
    return prodImage.publish("ttl.sh/myapp:latest");
  }
}
