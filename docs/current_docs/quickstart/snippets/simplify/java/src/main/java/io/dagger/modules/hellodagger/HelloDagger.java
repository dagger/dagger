package io.dagger.modules.hellodagger;

import io.dagger.client.*;
import io.dagger.module.AbstractModule;
import io.dagger.module.annotation.DefaultPath;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

/** HelloDagger main object */
@Object
public class HelloDagger extends AbstractModule {
  /** Publish the application container after building and testing it on-the-fly */
  @Function
  public String publish(@DefaultPath("/") Directory source)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    this.test(source);
    return build(source)
        .publish("ttl.sh/hello-dagger-%d".formatted((int) (Math.random() * 10000000)));
  }

  /** Build the application container */
  @Function
  public Container build(@DefaultPath("/") Directory source) {
    Directory build =
        dag.node(new Client.NodeArguments().withCtr(buildEnv(source)))
            .commands()
            .run(List.of("build"))
            .directory("./dist");
    return dag.container()
        .from("nginx:1.25-alpine")
        .withDirectory("/usr/share/nginx/html", build)
        .withExposedPort(80);
  }

  /** Return the result of running unit tests */
  @Function
  public String test(@DefaultPath("/") Directory source)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    return dag.node(new Client.NodeArguments().withCtr(buildEnv(source)))
        .commands()
        .run(List.of("test:unit", "run"))
        .stdout();
  }

  /** Build a ready-to-use development environment */
  @Function
  public Container buildEnv(@DefaultPath("/") Directory source) {
    CacheVolume nodeCache = dag.cacheVolume("node");
    return dag.node(new Client.NodeArguments().withVersion("21"))
        .withNpm()
        .withSource(source)
        .install()
        .container();
  }
}
