package io.dagger.modules.fields;

import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;

@Object
public class Fields {
  @Function
  private String version;

  public String publicVersion;

  private String internalVersion;

  public Fields() {}

  @Function
  public Fields withVersion(String version) {
    this.version = version;
    this.internalVersion = version;
    this.publicVersion = version;
    return this;
  }

  @Function
  public String getVersion() {
    return version;
  }

  @Function
  public String getInternalVersion() {
    return internalVersion;
  }
}
