package io.dagger.modules.mymodule;

import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;

@Object
public class MyModule {
  public String greeting;
  public String name;

  @Function
  public MyModule withGreeting(String greeting) {
    this.greeting = greeting;
    return this;
  }

  @Function
  public MyModule withName(String name) {
    this.name = name;
    return this;
  }

  @Function
  public String message() {
    return "%s, %s!".formatted(
        greeting != null ? greeting : "Hello",
        name != null ? name : "World");
  }
}
