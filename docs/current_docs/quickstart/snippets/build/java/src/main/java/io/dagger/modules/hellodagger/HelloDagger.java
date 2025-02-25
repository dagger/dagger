package io.dagger.modules.hellodagger;

import io.dagger.client.Container;
import io.dagger.client.DaggerQueryException;
import io.dagger.client.Directory;
import io.dagger.client.CacheVolume;
import io.dagger.module.AbstractModule;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

/** HelloDagger main object */
@Object
public class HelloDagger extends AbstractModule {
  /** Build the application container */
  @Function
  public Container build(Directory source)
      throws InterruptedException, ExecutionException, DaggerQueryException {
    Directory build = this
        // get the build environment container
        // by calling another Dagger Function
        .buildEnv(source)
        // build the application
        .withExec(List.of("npm", "run", "build"))
        // get the build output directory
        .directory("./dist");
    return dag.container()
        // start from a slim NGINX container
        .from("nginx:1.25-alpine")
        // copy the build output directory to the container
        .withDirectory("/usr/share/nginx/html", build)
        // expose the container port
        .withExposedPort(80);
  }
}
