package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.Random;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  /** Build and publish image with OCI annotations */
  @Function
  public String build() throws ExecutionException, DaggerQueryException, InterruptedException {
    return dag()
        .container()
        .from("alpine:latest")
        .withExec(List.of("apk", "add", "git"))
        .withWorkdir("/src")
        .withExec(List.of("git", "clone", "https://github.com/dagger/dagger", "."))
        .withAnnotation("org.opencontainers.image.authors", "John Doe")
        .withAnnotation("org.opencontainers.image.title", "Dagger source image viewer")
        .publish("ttl.sh/custom-image-%d".formatted(new Random().nextInt(10000000)));
  }
}
