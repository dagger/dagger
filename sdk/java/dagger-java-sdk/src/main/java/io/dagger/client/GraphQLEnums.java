package io.dagger.client;

/** Converts enum constants to and from the values the API knows them by. */
public final class GraphQLEnums {
  private GraphQLEnums() {}

  /**
   * The value an enum constant is sent as: its {@link GraphQLEnum#graphQLValue()}, or its name for
   * an enum named like its values (including module enums).
   */
  public static String value(Enum<?> constant) {
    if (constant instanceof GraphQLEnum graphQLEnum) {
      return graphQLEnum.graphQLValue();
    }
    return constant.name();
  }

  /**
   * The constant of an enum class the API value stands for: the one with that {@link
   * GraphQLEnum#hasGraphQLValue value}, or the one with that name.
   *
   * @throws IllegalArgumentException if no constant matches
   */
  @SuppressWarnings({"unchecked", "rawtypes"})
  public static <T> T valueOf(Class<T> enumClass, String value) {
    if (!enumClass.isEnum()) {
      throw new IllegalArgumentException(enumClass.getName() + " is not an enum");
    }
    if (GraphQLEnum.class.isAssignableFrom(enumClass)) {
      for (T constant : enumClass.getEnumConstants()) {
        if (((GraphQLEnum) constant).hasGraphQLValue(value)) {
          return constant;
        }
      }
    }
    return (T) Enum.valueOf((Class) enumClass, value);
  }
}
