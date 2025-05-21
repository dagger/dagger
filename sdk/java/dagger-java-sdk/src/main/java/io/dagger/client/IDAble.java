package io.dagger.client;

import jakarta.json.bind.annotation.JsonbTypeSerializer;
import java.util.concurrent.ExecutionException;
import io.dagger.client.exceptions.DaggerQueryException;

@JsonbTypeSerializer(IDAbleSerializer.class)
public interface IDAble<S> {

  S id() throws ExecutionException, InterruptedException, DaggerQueryException;
}
