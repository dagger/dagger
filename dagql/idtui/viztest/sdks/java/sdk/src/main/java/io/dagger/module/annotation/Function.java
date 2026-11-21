package io.dagger.module.annotation;

import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

@Target({ElementType.METHOD, ElementType.FIELD})
@Retention(RetentionPolicy.SOURCE)
public @interface Function {
  String value() default "";

  String description() default "";

  /**
   * How the engine caches the function's result: {@code "never"}, {@code "session"} for the
   * duration of a client session, or a duration such as {@code "10m"} after which the result
   * expires. Empty keeps the engine's default policy.
   */
  String cache() default "";
}
