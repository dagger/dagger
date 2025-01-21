package io.dagger.module.info;

public record ObjectInfo(
    String name, String qualifiedName, String description, FunctionInfo[] functions) {}
