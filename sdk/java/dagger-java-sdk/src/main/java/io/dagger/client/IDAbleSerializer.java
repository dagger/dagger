package io.dagger.client;

import jakarta.json.bind.serializer.JsonbSerializer;
import jakarta.json.bind.serializer.SerializationContext;
import jakarta.json.stream.JsonGenerator;
import java.util.concurrent.ExecutionException;

public class IDAbleSerializer<S> implements JsonbSerializer<IDAble<S>> {
  @Override
  public void serialize(IDAble<S> obj, JsonGenerator generator, SerializationContext ctx) {
    try {
      var id = obj.id();
      if (id.getClass().isAssignableFrom(Scalar.class)) {
        ctx.serialize(((Scalar) id), generator);
      } else {
        ctx.serialize(id, generator);
      }
    } catch (ExecutionException | InterruptedException | DaggerQueryException e) {
      throw new RuntimeException(e);
    }
  }
}
