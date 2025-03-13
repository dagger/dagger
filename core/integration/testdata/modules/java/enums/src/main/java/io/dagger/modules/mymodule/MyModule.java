package io.dagger.modules.mymodule;

import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;

@Object
public class MyModule {
  @Function
  public String print(Severity severity) {
    return severity.name();
  }

  @Function
  public Severity fromString(String severity) {
    return Severity.valueOf(severity);
  }

  @Function
  public List<Severity> getSeverities() {
    return Arrays.asList(Severity.values());
  }

  @Function
  public String toString(List<Severity> severities) {
    return severities.stream().map(Severity::name).collect(Collectors.joining(","));
  }
}
