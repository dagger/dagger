package io.dagger.client;

import jakarta.json.bind.serializer.JsonbSerializer;
import jakarta.json.bind.serializer.SerializationContext;
import jakarta.json.stream.JsonGenerator;

public class ScalarSerializer<T> implements JsonbSerializer<Scalar<T>> {
  @Override
  public void serialize(Scalar<T> obj, JsonGenerator generator, SerializationContext ctx) {
    ctx.serialize(obj.convert(), generator);
  }
}
