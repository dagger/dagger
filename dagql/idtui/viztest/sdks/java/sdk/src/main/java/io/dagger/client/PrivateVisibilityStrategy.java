package io.dagger.client;

import jakarta.json.bind.config.PropertyVisibilityStrategy;
import java.lang.reflect.Field;
import java.lang.reflect.Method;

class PrivateVisibilityStrategy implements PropertyVisibilityStrategy {

  /** As in {@link FieldsStrategy}, java.base keeps the fields of {@link Enum} closed. */
  @Override
  public boolean isVisible(Field field) {
    return field.getDeclaringClass() != Enum.class;
  }

  @Override
  public boolean isVisible(Method method) {
    return true;
  }
}
