package io.dagger.modules.hellowithservicesjava;

import io.dagger.client.Dagger;
import io.dagger.client.Service;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import io.dagger.module.annotation.Up;

/** A module for HelloWithServicesJava functions */
@Object
public class HelloWithServicesJava {

  /** Returns a web server service */
  @Function
  @Up
  public Service web() {
    return Dagger.dag()
      .container()
      .from("nginx:alpine")
      .withExposedPort(80)
      .asService();
  }

  /** Returns a redis service */
  @Function
  @Up
  public Service redis() {
    return Dagger.dag()
      .container()
      .from("redis:alpine")
      .withExposedPort(6379)
      .asService();
  }

  @Function
  public Infra infra() {
    return new Infra();
  }
}
