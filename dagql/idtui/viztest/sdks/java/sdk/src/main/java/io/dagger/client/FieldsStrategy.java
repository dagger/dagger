package io.dagger.client;

import jakarta.json.bind.config.PropertyVisibilityStrategy;
import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.lang.reflect.Modifier;

public class FieldsStrategy implements PropertyVisibilityStrategy {
  /**
   * Yasson asks about the fields of an enum it binds as a list element, and java.base keeps the
   * fields of {@link Enum} closed, so exposing them fails the whole conversion.
   */
  @Override
  public boolean isVisible(Field field) {
    return field.getDeclaringClass() != Enum.class
        && !Modifier.isTransient(field.getModifiers())
        && !Modifier.isStatic(field.getModifiers());
  }

  @Override
  public boolean isVisible(Method method) {
    return false;
  }
}
