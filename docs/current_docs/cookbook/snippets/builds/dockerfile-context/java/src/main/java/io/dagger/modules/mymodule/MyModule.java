package io.dagger.modules.mymodule;

import io.dagger.client.Container;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.client.Directory;
import io.dagger.client.File;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.concurrent.ExecutionException;

import static io.dagger.client.Dagger.dag;

@Object
public class MyModule {
  /**
   * Build and publish image from Dockerfile using a build context directory in a different location
   * than the current working directory
   *
   * @param src location of source directory
   * @param dockerfile location of Dockerfile
   */
  @Function
  public String build(Directory src, File dockerfile)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    // get build context with dockerfile added
    Directory workspace =
        dag()
            .container()
            .withDirectory("/src", src)
            .withWorkdir("/src")
            .withFile("/src/custom.Dockerfile", dockerfile)
            .directory("/src");

    // build using Dockerfile and push to registry
    return dag()
        .container()
        .build(workspace, new Container.BuildArguments().withDockerfile("custom.Dockerfile"))
        .publish("ttl.sh/hello-dagger");
  }
}
