package io.dagger.modules.mymodule;

import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;

import java.util.Arrays;
import java.util.List;
import java.util.stream.Collectors;

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
  public List<Severity> getSeveritiesList() {
    return Arrays.asList(Severity.values());
  }

  @Function
  public Severity[] getSeveritiesArray() {
    return Severity.values();
  }

  @Function
  public String listToString(List<Severity> severities) {
    return severities.stream().map(Severity::name).collect(Collectors.joining(","));
  }

  @Function
  public String arrayToString(Severity[] severities) {
    return Arrays.stream(severities).map(Severity::name).collect(Collectors.joining(","));
  }
}
