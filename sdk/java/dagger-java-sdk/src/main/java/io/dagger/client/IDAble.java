package io.dagger.client;

import java.util.concurrent.ExecutionException;

public interface IDAble<S> {

  S id() throws ExecutionException, InterruptedException, DaggerQueryException;
}
