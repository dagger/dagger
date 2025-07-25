package io.dagger.modules.mymodule;

import io.dagger.module.annotation.Default;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;

@Object
public class MyModule {
  @Function
  public String hello(@Default("world") String name) {
    return "Hello, %s".formatted(name);
  }
}
