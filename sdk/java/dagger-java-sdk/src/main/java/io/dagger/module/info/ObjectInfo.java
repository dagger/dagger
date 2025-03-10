package io.dagger.module.info;

import java.util.Optional;

public record ObjectInfo(
    String name,
    String qualifiedName,
    String description,
    FieldInfo[] fields,
    FunctionInfo[] functions,
    Optional<FunctionInfo> constructor) {}
