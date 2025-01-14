package io.dagger.client;

import jakarta.json.bind.JsonbBuilder;

public class Scalar<T> {

  private final T value;

  protected Scalar(T value) {
    this.value = value;
  }

  public T convert() {
    return value;
  }

  public JSON toJSON() throws Exception {
    try (var jsonb = JsonbBuilder.create()) {
      return JSON.from(jsonb.toJson(value));
    }
  }
}
