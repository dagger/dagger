package io.dagger.modules.hellowithservicesjava;

import io.dagger.client.Dagger;
import io.dagger.client.Service;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import io.dagger.module.annotation.Up;

@Object
public class Infra {

  /** Returns a postgres database service */
  @Function
  @Up
  public Service database() {
    return Dagger.dag()
      .container()
      .from("postgres:alpine")
      .withEnvVariable("POSTGRES_PASSWORD", "test")
      .withExposedPort(5432)
      .asService();
  }
}
