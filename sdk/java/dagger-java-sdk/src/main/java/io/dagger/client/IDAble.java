package io.dagger.client;

import io.dagger.client.exception.DaggerExecException;
import io.dagger.client.exception.DaggerQueryException;
import jakarta.json.bind.annotation.JsonbTypeSerializer;
import java.util.concurrent.ExecutionException;

@JsonbTypeSerializer(IDAbleSerializer.class)
public interface IDAble<S> {

  S id() throws ExecutionException, InterruptedException, DaggerQueryException, DaggerExecException;
}
