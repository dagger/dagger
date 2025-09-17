package io.dagger.modules.mymodule;


import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;

@Object
public class MyModule {
  @Function
  public int divide(int a, int b) {
    if (b == 0) {
      throw new IllegalArgumentException("cannot divide by zero");
    }
    return a / b;
  }
}
