package io.dagger.module.info;

public record FunctionInfo(
    String name,
    String qName,
    String description,
    String cache,
    TypeInfo returnType,
    ParameterInfo[] parameters,
    boolean isCheck,
    boolean isGenerate,
    boolean isUp,
    boolean isGet) {}
