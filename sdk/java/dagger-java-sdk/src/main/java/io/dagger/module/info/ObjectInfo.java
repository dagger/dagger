package io.dagger.module.info;

import java.util.Optional;

public record ObjectInfo(
    String name,
    String qualifiedName,
    String description,
    boolean collection,
    FieldInfo[] fields,
    FunctionInfo[] functions,
    Optional<FunctionInfo> constructor) {}
