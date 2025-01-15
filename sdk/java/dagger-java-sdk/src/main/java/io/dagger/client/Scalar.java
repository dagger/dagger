package io.dagger.client;

import com.google.gson.Gson;
import com.google.gson.annotations.Expose;

public class Scalar<T> {

  @Expose
  private T value;

  protected Scalar(T value) {
    this.value = value;
  }

  T convert() {
    return value;
  }

  public JSON toJSON() throws Exception {
    Gson gson = new Gson();
    String json = gson.toJson(value);
    return JSON.from(json);
  }
}
