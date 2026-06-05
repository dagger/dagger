package io.dagger.client;

import jakarta.json.bind.serializer.DeserializationContext;
import jakarta.json.bind.serializer.JsonbDeserializer;
import jakarta.json.stream.JsonParser;
import java.lang.reflect.Type;

public class ScalarStringDeserializer implements JsonbDeserializer<Scalar<String>> {

  @Override
  public Scalar<String> deserialize(JsonParser jsonParser, DeserializationContext ctx, Type type) {
    String value = ctx.deserialize(String.class, jsonParser);
    return new Scalar<>(value);
  }
}
