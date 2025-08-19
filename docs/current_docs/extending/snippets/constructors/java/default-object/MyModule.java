package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.Optional;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {

  public Container ctr;

  public MyModule() {}

  public MyModule(Optional<Container> ctr) {
    this.ctr = ctr.orElseGet(() -> dag().container().from("alpine:3.14.0"));
  }

  @Function
  public String version() throws ExecutionException, DaggerQueryException, InterruptedException {
    return ctr.withExec(List.of("/bin/sh", "-c", "cat /etc/os-release | grep VERSION_ID")).stdout();
  }
}
