package io.dagger.modules.mymodule;

import io.dagger.module.AbstractModule;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;

@Object
public class MyModule extends AbstractModule {
  @Function
  public String hello(String[] names) {
    String message = "Hello";
    if (names.length > 0) {
      message += " " + String.join(", ", names);
    }
    return message;
  }
}
