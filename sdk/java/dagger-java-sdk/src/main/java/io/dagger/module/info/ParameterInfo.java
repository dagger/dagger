package io.dagger.module.info;

public record ParameterInfo(
    String name, String description, String type, boolean optional, String defaultValue) {}
