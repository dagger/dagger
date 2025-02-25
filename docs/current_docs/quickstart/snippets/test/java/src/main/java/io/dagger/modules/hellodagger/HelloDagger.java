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
  /** Return the result of running unit tests */
  @Function
  public String test(Directory source)
      throws InterruptedException, ExecutionException, DaggerQueryException {
        // get the build environment container
        // by calling another Dagger Function
    return this
        .buildEnv(source)
        // call the test runner
        .withExec(List.of("npm", "run", "test:unit", "run"))
        // capture and return the command output
        .stdout();
  }
}
