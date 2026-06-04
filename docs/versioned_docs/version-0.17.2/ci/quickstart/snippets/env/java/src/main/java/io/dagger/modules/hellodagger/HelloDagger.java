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
  /** Build a ready-to-use development environment */
  @Function
  public Container buildEnv(@DefaultPath("/") Directory source)
      throws InterruptedException, ExecutionException, DaggerQueryException {
    CacheVolume nodeCache = dag().cacheVolume("node");
    return dag().container()
        // start from a base Node.js container
        .from("node:21-slim")
        // add the source code at /src
        .withDirectory("/src", source)
        // mount the cache volume at /root/.npm
        .withMountedCache("/root/.npm", nodeCache)
        // change the working directory to /src
        .withWorkdir("/src")
        // run npm install to install dependencies
        .withExec(List.of("npm", "install"));
  }
}
