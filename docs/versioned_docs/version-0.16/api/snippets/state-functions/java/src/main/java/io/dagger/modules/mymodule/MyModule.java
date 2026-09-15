package io.dagger.modules.mymodule;

import io.dagger.module.annotation.Default;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;

@Object
public class MyModule {
  /** The greeting to use */
  public String greeting;

  /** Who to greet */
  private String name;

  public MyModule() {}

  /**
   * @param greeting The greeting to use
   * @param name Who to greet
   */
  public MyModule(@Default("Hello") String greeting, @Default("World") String name) {
    this.greeting = greeting;
    this.name = name;
  }

  /** Return the greeting message */
  @Function
  public String message() {
    return greeting + ", " + name;
  }
}
