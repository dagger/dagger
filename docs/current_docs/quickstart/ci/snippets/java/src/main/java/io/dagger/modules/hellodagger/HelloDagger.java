package io.dagger.modules.hellodagger;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.DaggerQueryException;
import io.dagger.client.Directory;
import io.dagger.client.CacheVolume;
import io.dagger.module.annotation.DefaultPath;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

/** HelloDagger main object */
@Object
public class HelloDagger {
  /** Publish the application container after building and testing it on-the-fly */
  @Function
  public String publish(@DefaultPath("/") Directory source)
      throws InterruptedException, ExecutionException, DaggerQueryException {
    this.test(source);
    return this.build(source).
        publish("ttl.sh/hello-dagger-%d".formatted((int) (Math.random() * 10000000)));
  }

  /** Build the application container */
  @Function
  public Container build(@DefaultPath("/") Directory source)
      throws InterruptedException, ExecutionException, DaggerQueryException {
    Directory build = this
        .buildEnv(source)
        .withExec(List.of("npm", "run", "build"))
        .directory("./dist");
    return dag().container()
        .from("nginx:1.25-alpine")
        .withDirectory("/usr/share/nginx/html", build)
        .withExposedPort(80);
  }

  /** Return the result of running unit tests */
  @Function
  public String test(@DefaultPath("/") Directory source)
      throws InterruptedException, ExecutionException, DaggerQueryException {
    return this
        .buildEnv(source)
        .withExec(List.of("npm", "run", "test:unit", "run"))
        .stdout();
  }

  /** Build a ready-to-use development environment */
  @Function
  public Container buildEnv(@DefaultPath("/") Directory source)
      throws InterruptedException, ExecutionException, DaggerQueryException {
    CacheVolume nodeCache = dag().cacheVolume("node");
    return dag().container()
        .from("node:21-slim")
        .withDirectory("/src", source)
        .withMountedCache("/root/.npm", nodeCache)
        .withWorkdir("/src")
        .withExec(List.of("npm", "install"));
  }
}
