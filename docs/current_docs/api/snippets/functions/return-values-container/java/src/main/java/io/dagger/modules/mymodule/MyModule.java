package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;

import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  @Function
  public Container alpineBuilder(List<String> packages) {
    Container ctr = dag().container().from("alpine:latest");
    for (String pkg : packages) {
      ctr = ctr.withExec(List.of("apk", "add", pkg));
    }
    return ctr;
  }
}
