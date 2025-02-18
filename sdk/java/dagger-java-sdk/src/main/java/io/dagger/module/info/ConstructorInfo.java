package io.dagger.module.info;

public record ConstructorInfo(boolean hasDaggerClient, FunctionInfo constructor) {}
