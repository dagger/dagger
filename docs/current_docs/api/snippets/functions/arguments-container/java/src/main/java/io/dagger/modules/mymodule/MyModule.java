package io.dagger.modules.mymodule;

import io.dagger.client.Container;
import io.dagger.client.DaggerQueryException;
import io.dagger.module.AbstractModule;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;

import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule extends AbstractModule {
  @Function
  public String osInfo(Container ctr)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    return ctr.withExec(List.of("uname", "-a")).stdout();
  }
}
