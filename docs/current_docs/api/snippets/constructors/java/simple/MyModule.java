package io.dagger.modules.mymodule;

import io.dagger.module.annotation.Default;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;

@Object
public class MyModule {
  private String greeting;
  private String name;

  public MyModule() {}

  public MyModule(@Default("Hello") String greeting, @Default("World") String name) {
    this.greeting = greeting;
    this.name = name;
  }

  @Function
  public String message() {
    return greeting + " " + name;
  }
}
