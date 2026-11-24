package io.dagger.client;

import jakarta.json.bind.JsonbException;
import jakarta.json.bind.serializer.DeserializationContext;
import jakarta.json.bind.serializer.JsonbDeserializer;
import jakarta.json.stream.JsonParser;
import java.lang.reflect.Type;

public class ScalarStringDeserializer implements JsonbDeserializer<Scalar<String>> {

  @Override
  @SuppressWarnings("unchecked")
  public Scalar<String> deserialize(JsonParser jsonParser, DeserializationContext ctx, Type type) {
    String value = ctx.deserialize(String.class, jsonParser);
    try {
      return (Scalar<String>)
          ((Class<?>) type).getDeclaredConstructor(String.class).newInstance(value);
    } catch (ReflectiveOperationException e) {
      throw new JsonbException("cannot build the scalar " + type.getTypeName(), e);
    }
  }
}
