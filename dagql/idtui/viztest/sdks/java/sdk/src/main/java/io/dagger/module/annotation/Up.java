package io.dagger.module.annotation;

import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * Annotation to mark a function as a service for {@code dagger up}.
 *
 * <p>Functions annotated with {@code @Up} will be discovered by {@code dagger up} and their
 * returned Service will be started and tunneled to the host.
 */
@Target({ElementType.METHOD})
@Retention(RetentionPolicy.RUNTIME)
public @interface Up {}
