package io.dagger.module.info;

public record FunctionInfo(
    String name,
    String qName,
    String description,
    TypeInfo returnType,
    ParameterInfo[] parameters) {}
