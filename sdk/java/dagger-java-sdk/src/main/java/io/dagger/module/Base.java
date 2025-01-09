package io.dagger.module;

import io.dagger.client.Client;

public class Base {
    protected final Client dag;

    public Base(Client dag) {
        this.dag = dag;
    }
}
