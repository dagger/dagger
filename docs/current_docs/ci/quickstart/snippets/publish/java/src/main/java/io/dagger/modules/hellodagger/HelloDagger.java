package io.dagger.modules.hellodagger;

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
    // call Dagger Function to run unit tests
    this.test(source);
    // call Dagger Function to build the application image
    // publish the image to ttl.sh
    return this.build(source).
        publish("ttl.sh/hello-dagger-%d".formatted((int) (Math.random() * 10000000)));
  }
}
