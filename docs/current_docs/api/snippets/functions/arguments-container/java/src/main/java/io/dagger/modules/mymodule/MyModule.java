package io.dagger.modules.mymodule;

import io.dagger.client.Container;
import io.dagger.client.exception.DaggerExecException;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.Function;


import io.dagger.module.annotation.Object;

import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  @Function
  public String osInfo(Container ctr)
      throws ExecutionException, DaggerExecException, DaggerQueryException, InterruptedException {
    return ctr.withExec(List.of("uname", "-a")).stdout();
  }
}
