package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.time.ZonedDateTime;
import java.time.format.DateTimeFormatter;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  /** Build and publish image with OCI labels */
  @Function
  public String build() throws ExecutionException, DaggerQueryException, InterruptedException {
    return dag()
        .container()
        .from("alpine")
        .withLabel("org.opencontainers.image.title", "my-alpine")
        .withLabel("org.opencontainers.image.version", "1.0")
        .withLabel(
            "org.opencontainers.image.created",
            ZonedDateTime.now().format(DateTimeFormatter.ISO_INSTANT))
        .withLabel(
            "org.opencontainers.image.source", "https://github.com/alpinelinux/docker-alpine")
        .withLabel("org.opencontainers.image.licenses", "MIT")
        .publish("ttl.sh/my-alpine");
  }
}
