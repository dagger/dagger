package io.dagger.module.info;

public record FunctionInfo(
    String name, String qName, String description, String returnType, ParameterInfo[] parameters) {}
