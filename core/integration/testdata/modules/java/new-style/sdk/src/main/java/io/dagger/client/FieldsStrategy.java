package io.dagger.client;

import jakarta.json.bind.config.PropertyVisibilityStrategy;
import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.lang.reflect.Modifier;

public class FieldsStrategy implements PropertyVisibilityStrategy {
  @Override
  public boolean isVisible(Field field) {
    return !Modifier.isTransient(field.getModifiers()) && !Modifier.isStatic(field.getModifiers());
  }

  @Override
  public boolean isVisible(Method method) {
    return false;
  }
}
