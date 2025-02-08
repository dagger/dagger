package io.dagger.modules.fields;

import io.dagger.module.AbstractModule;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;

@Object
public class Fields extends AbstractModule {
  public String version;

  public Fields() {
    super();
  }

  @Function
  public Fields withVersion(String version) {
    this.version = version;
    return this;
  }

  @Function
  public String getVersion() {
    return version;
  }
}
