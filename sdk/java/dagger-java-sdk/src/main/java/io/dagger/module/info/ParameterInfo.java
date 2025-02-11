package io.dagger.module.info;

import java.util.Optional;

public record ParameterInfo(
    String name,
    String description,
    TypeInfo type,
    boolean optional,
    Optional<String> defaultValue,
    Optional<String> defaultPath) {}
