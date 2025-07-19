package io.dagger.modules.mymodule;

import io.dagger.client.exception.DaggerQueryException;
import io.dagger.client.Directory;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  /**
   * Build and publish image from existing Dockerfile
   *
   * @param src location of directory containing Dockerfile
   */
  @Function
  public String build(Directory src)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    return src.dockerBuild().publish("ttl.sh/hello-dagger");
  }
}
