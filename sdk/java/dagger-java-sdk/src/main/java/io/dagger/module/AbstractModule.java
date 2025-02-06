package io.dagger.module;

import io.dagger.client.Client;

public abstract class AbstractModule {
  protected transient Client dag;

  public void setClient(Client dag) {
    this.dag = dag;
  }

  public AbstractModule(Client dag) {
    this.dag = dag;
  }

  public AbstractModule() {}
}
