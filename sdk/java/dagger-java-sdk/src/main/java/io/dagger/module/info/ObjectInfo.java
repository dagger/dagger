package io.dagger.module.info;

public record ObjectInfo(
    String name,
    String qualifiedName,
    String description,
    FieldInfo[] fieldInfos,
    FunctionInfo[] functions,
    ConstructorInfo constructorInfo) {}
