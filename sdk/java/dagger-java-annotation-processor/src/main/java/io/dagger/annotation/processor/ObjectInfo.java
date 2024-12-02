package io.dagger.annotation.processor;

public record  ObjectInfo(String name, String qualifiedName, String description, FunctionInfo[] functions) { }
