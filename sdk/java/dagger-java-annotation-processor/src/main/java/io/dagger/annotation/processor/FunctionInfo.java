package io.dagger.annotation.processor;

public record  FunctionInfo (String name, String qName, String description, String returnType, ParameterInfo[] parameters) { }
