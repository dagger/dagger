package io.dagger.client;

/**
 * A generated enum whose constants follow Java's naming convention ({@code PER_SESSION}) rather
 * than the schema's values ({@code PerSession}). Each constant carries the value the API knows it
 * by. Enums whose constants are named like their values don't implement it: they go over the wire
 * by name.
 */
public interface GraphQLEnum {
  /** The schema value this constant is sent as. */
  String graphQLValue();

  /**
   * Whether the API may return this constant as the given value: its own, or that of an alias the
   * schema declares for it ({@code GZIP} for {@code Gzip}).
   */
  default boolean hasGraphQLValue(String value) {
    return graphQLValue().equals(value);
  }
}
