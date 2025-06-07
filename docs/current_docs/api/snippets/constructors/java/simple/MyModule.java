package io.dagger.modules.mymodule;

import java.util.concurrent.ExecutionExcep
import java.util.List;
import java.util.concurrent.ExecutionException;
import io.dagger.client.Secret;
import io.dagger.client.exception.DaggerExecException;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.Function;

@Object
public class MyModule {
  @Function
  public Container foo(@Ignore({"*", "!src/**/*.java", "!pom.xml"}) Directory source)
      throws ExecutionException, DaggerExecException, DaggerQueryException, InterruptedException {
    return dag().container().from("alpine:latest").withDirectory("/src", source).sync();
  }
}
