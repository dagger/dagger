package io.dagger.client;

public class Scalar<T> {

  private T value;

  protected Scalar(T value) {
    this.value = value;
  }

  T convert() {
    return value;
  }

  public boolean eq(Scalar<T> other) {
    return value.equals(other.value);
  }
}
