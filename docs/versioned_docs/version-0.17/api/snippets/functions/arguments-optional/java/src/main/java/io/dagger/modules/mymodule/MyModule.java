package io.dagger.modules.mymodule;

import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.Optional;

@Object
public class MyModule {
  @Function
  public String hello(Optional<String> name) {
    return "Hello, " + name.orElse("World");
  }
}
