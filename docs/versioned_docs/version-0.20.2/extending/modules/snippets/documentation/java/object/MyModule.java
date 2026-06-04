package io.dagger.modules.mymodule;

import io.dagger.module.annotation.Object;

/** The object represents a single user of the system. */
@Object
public class MyModule {
  private String name;
  private int age;

  public MyModule() {}

  /**
   * @param name The name of the user.
   * @param age The age of the user.
   */
  public MyModule(String name, int age) {
    this.name = name;
    this.age = age;
  }
}
