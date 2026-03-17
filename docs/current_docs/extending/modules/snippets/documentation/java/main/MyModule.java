package io.dagger.modules.mymodule;

import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;

@Object
public class MyModule {
  /**
   * Return a greeting.
   *
   * @param name Who to greet
   * @param greeting The greeting to display
   */
  @Function
  public String hello(String name, String greeting) {
    return greeting + " " + name;
  }

  /**
   * Return a loud greeting.
   *
   * @param name Who to greet
   * @param greeting The greeting to display
   */
  @Function
  public String loudHello(String name, String greeting) {
    return greeting.toUpperCase() + " " + name.toUpperCase();
  }
}
