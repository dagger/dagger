package org.chelonix.dagger.client;

import java.util.concurrent.ExecutionException;

public interface IdProvider<S> {

    S id() throws ExecutionException, InterruptedException, DaggerQueryException;
}
