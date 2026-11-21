package io.dagger.client;

import java.util.ArrayList;
import java.util.Collection;
import java.util.List;

/** Converts enum constants to and from the values the API knows them by. */
public final class GraphQLEnums {
  private GraphQLEnums() {}

  /**
   * The value an enum constant is sent as: its {@link GraphQLEnum#graphQLValue()}, or its name for
   * an enum named like its values (including a module's own enums).
   */
  public static String value(Enum<?> constant) {
    if (constant instanceof GraphQLEnum graphQLEnum) {
      return graphQLEnum.graphQLValue();
    }
    return constant.name();
  }

  /**
   * An object to serialize, with every enum constant replaced by its {@link #value}: the object
   * itself, or the elements of a collection or an array, at any depth. JSON-B applies an enum's
   * serializer to a field, but not to an element of a list it serializes.
   */
  public static Object values(Object object) {
    if (object instanceof Enum<?> constant) {
      return value(constant);
    }
    if (object instanceof Collection<?> collection) {
      List<Object> values = new ArrayList<>(collection.size());
      collection.forEach(element -> values.add(values(element)));
      return values;
    }
    if (object instanceof Object[] array) {
      List<Object> values = new ArrayList<>(array.length);
      for (Object element : array) {
        values.add(values(element));
      }
      return values;
    }
    return object;
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
