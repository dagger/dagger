package io.dagger.client;

import java.lang.annotation.Documented;
import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * The GraphQL type a generated class stands for, when the class name follows Java's naming
 * convention rather than the schema's ({@code JsonValue} for {@code JSONValue}, {@code Llm} for
 * {@code LLM}). Everything that sends a type name to the API reads it: loading objects by ID and
 * registering module types. A class without it is named like its GraphQL type.
 */
@Documented
@Retention(RetentionPolicy.RUNTIME)
@Target(ElementType.TYPE)
public @interface GraphQLType {
  /** The GraphQL type name. */
  String value();
}
