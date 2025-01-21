package io.dagger.module;

import io.dagger.client.Client;

public class Base {
  protected transient Client dag;

  public void setClient(Client dag) {
    this.dag = dag;
  }

  public Base(Client dag) {
    this.dag = dag;
  }

  public Base() {}
}
