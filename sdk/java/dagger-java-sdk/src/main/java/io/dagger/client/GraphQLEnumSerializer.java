package io.dagger.client;

import jakarta.json.bind.serializer.JsonbSerializer;
import jakarta.json.bind.serializer.SerializationContext;
import jakarta.json.stream.JsonGenerator;

/** Serializes a {@link GraphQLEnum} constant as its schema value. */
public class GraphQLEnumSerializer implements JsonbSerializer<GraphQLEnum> {
  @Override
  public void serialize(GraphQLEnum obj, JsonGenerator generator, SerializationContext ctx) {
    generator.write(obj.graphQLValue());
  }
}
