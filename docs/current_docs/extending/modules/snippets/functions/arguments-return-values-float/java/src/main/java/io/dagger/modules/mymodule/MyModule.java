package io.dagger.modules.mymodule;

import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;

@Object
public class MyModule {
  @Function
  public float addFloat(float a, float b) {
    return a + b;
  }
}
