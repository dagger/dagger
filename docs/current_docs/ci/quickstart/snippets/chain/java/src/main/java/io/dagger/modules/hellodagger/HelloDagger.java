package io.dagger.modules.hellodagger;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.DaggerQueryException;
import io.dagger.client.Directory;
import io.dagger.client.CacheVolume;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

/** HelloDagger main object */
@Object
public class HelloDagger {
  /**
   * Returns a base container
   */
  @Function
  public Container base() {
    return dag().container().from("cgr.dev/chainguard/wolfi-base");
  }

  /**
   * Builds on top of base container and returns a new container
   */
  @Function
  public Container build() {
    return this.base().withExec(List.of("apk", "add", "bash", "git"));
  }

  /**
   * Builds and publishes a container
   */
  @Function
  public String buildAndPublish()
      throws InterruptedException, ExecutionException, DaggerQueryException {
    return this.build().publish("ttl.sh/bar");
  }
}
