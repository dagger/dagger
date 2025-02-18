package io.dagger.modules.daggermodule;

import io.dagger.client.Container;
import io.dagger.client.DaggerQueryException;
import io.dagger.client.Directory;
import io.dagger.module.AbstractModule;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

/** DaggerModule main object */
@Object
public class DaggerModule extends AbstractModule {
  /** Returns a container that echoes whatever string argument is provided */
  @Function
  public Container containerEcho(String stringArg) {
    return dag.container().from("alpine:latest").withExec(List.of("echo", stringArg));
  }

  /** Returns lines that match a pattern in the files of the provided Directory */
  @Function
  public String grepDir(Directory directoryArg, String pattern)
      throws InterruptedException, ExecutionException, DaggerQueryException {
    return dag.container()
        .from("alpine:latest")
        .withMountedDirectory("/mnt", directoryArg)
        .withWorkdir("/mnt")
        .withExec(List.of("grep", "-R", pattern, "."))
        .stdout();
  }
}
