package org.chelonix.dagger.model;

public class Scalar<T> {

    private final T value;

    public Scalar(T value) {
        this.value = value;
    }

    T convert() {
        return value;
    }
}
