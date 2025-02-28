package io.dagger.modules.construct;

import static io.dagger.client.Dagger.dag;

import io.dagger.module.AbstractModule;
import io.dagger.module.annotation.Default;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;

@Object
public class Construct {
  public String value;

  public Construct() {
    super();
  }

  public Construct(@Default("from constructor") String value) {
    super();
    this.value = value;
  }

  @Function
  public String echo() {
    return value;
  }
}
