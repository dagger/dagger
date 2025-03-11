package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.*;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.DefaultPath;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  public Directory source;

  public MyModule() {}

  public MyModule(@DefaultPath(".") Directory source) {
    this.source = source;
  }

  @Function
  public List<String> foo() throws ExecutionException, DaggerQueryException, InterruptedException {
    return dag()
        .container()
        .from("alpine:latest")
        .withMountedDirectory("/app", this.source)
        .directory("/app")
        .entries();
  }
}
