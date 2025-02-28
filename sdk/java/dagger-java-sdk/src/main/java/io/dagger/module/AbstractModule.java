package io.dagger.module;

import io.dagger.client.Client;
import io.dagger.client.Dagger;

/**
 * @deprecated use {@code Dagger.dag()} to get access to a Dagger client
 */
public abstract class AbstractModule {
  protected transient Client dag;

  public AbstractModule(Client _dag) {
    this.dag = Dagger.dag();
  }

  public AbstractModule() {
    this.dag = Dagger.dag();
  }
}
