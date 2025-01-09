package io.dagger.client;

public class Scalar<T> {

    private final T value;

    protected Scalar(T value) {
        this.value = value;
    }

    public T convert() {
        return value;
    }
}
