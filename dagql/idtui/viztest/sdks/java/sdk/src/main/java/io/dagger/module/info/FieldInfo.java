package io.dagger.module.info;

public record FieldInfo(
    String name, String description, TypeInfo type, boolean isKeys, boolean isDelta) {}
