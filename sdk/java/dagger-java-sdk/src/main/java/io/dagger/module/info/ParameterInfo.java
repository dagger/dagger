package io.dagger.module.info;

public record ParameterInfo(
    String name, String description, TypeInfo type, boolean optional, String defaultValue) {}
