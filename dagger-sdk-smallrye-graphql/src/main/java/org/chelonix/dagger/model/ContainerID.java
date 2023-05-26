package org.chelonix.dagger.model;

public class ContainerID extends Scalar<String> {

    public static ContainerID of(String value) {
        return new ContainerID(value);
    }

    public ContainerID(String value) {
        super(value);
    }
}
