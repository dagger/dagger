package io.dagger.client;

import jakarta.json.bind.config.PropertyVisibilityStrategy;
import java.lang.reflect.Field;
import java.lang.reflect.Method;

class PrivateVisibilityStrategy implements PropertyVisibilityStrategy {

  @Override
  public boolean isVisible(Field field) {
    return true;
  }

  @Override
  public boolean isVisible(Method method) {
    return true;
  }
}
