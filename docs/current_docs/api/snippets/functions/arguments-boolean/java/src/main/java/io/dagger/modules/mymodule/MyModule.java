package io.dagger.modules.mymodule;

import io.dagger.module.AbstractModule;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;

@Object
public class MyModule extends AbstractModule {
  @Function
  public String hello(boolean shout) {
    String message = "Hello, world";
    if (shout) {
      message = message.toUpperCase();
    }
    return message;
  }
}
