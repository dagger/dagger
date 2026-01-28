package io.dagger.modules.hellowithchecksjava;

import io.dagger.client.Container;
import io.dagger.client.Dagger;
import io.dagger.module.annotation.Check;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;

/** A module for HelloWithChecksJava functions */
@Object
public class HelloWithChecksJava {

  /** Returns a passing check */
  @Function
  @Check
  public void passingCheck() throws Exception {
    Container container = Dagger.dag()
      .container()
      .from("alpine:3")
      .withExec(List.of("sh", "-c", "exit 0"));
    container.sync();
  }

  /** Returns a failing check */
  @Function
  @Check
  public void failingCheck() throws Exception {
    Container container = Dagger.dag()
      .container()
      .from("alpine:3")
      .withExec(List.of("sh", "-c", "exit 1"));
    container.sync();
  }

  /** Returns a container which runs as a passing check */
  @Function
  @Check
  public Container passingContainer() throws Exception {
    return Dagger.dag()
      .container()
      .from("alpine:3")
      .withExec(List.of("sh", "-c", "exit 0"));
  }

  /** Returns a container which runs as a failing check */
  @Function
  @Check
  public Container failingContainer() throws Exception {
    return Dagger.dag()
      .container()
      .from("alpine:3")
      .withExec(List.of("sh", "-c", "exit 1"));
  }
}
