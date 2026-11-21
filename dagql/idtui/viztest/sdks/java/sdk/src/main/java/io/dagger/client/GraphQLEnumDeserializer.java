package io.dagger.client;

import jakarta.json.bind.serializer.DeserializationContext;
import jakarta.json.bind.serializer.JsonbDeserializer;
import jakarta.json.stream.JsonParser;
import java.lang.reflect.Type;

/** Deserializes a {@link GraphQLEnum} constant from its schema value. */
public class GraphQLEnumDeserializer implements JsonbDeserializer<Object> {
  @Override
  public Object deserialize(JsonParser parser, DeserializationContext ctx, Type type) {
    String value = ctx.deserialize(String.class, parser);
    if (value == null) {
      return null;
    }
    if (!(type instanceof Class<?> enumClass)) {
      throw new IllegalArgumentException("cannot deserialize an enum as " + type);
    }
    return GraphQLEnums.valueOf(enumClass, value);
  }
}
