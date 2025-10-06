package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.DaggerQueryException;
import io.dagger.client.Directory;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Ignore;
import io.dagger.module.annotation.Object;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  @Function
  public Container foo(@Ignore({"*", "!src/**/*.java", "!pom.xml"}) Directory source)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    return dag().container().from("alpine:latest").withDirectory("/src", source).sync();
  }
}
