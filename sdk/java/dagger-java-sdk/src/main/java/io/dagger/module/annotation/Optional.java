package io.dagger.module.annotation;

import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

@Target(ElementType.PARAMETER)
@Retention(RetentionPolicy.SOURCE)
public @interface Optional {
  /**
   * Default value of the parameter. This must be a <b>valid JSON string</b>.
   *
   * <p>Be careful with string values. For instance <code>defaultValue="true"</code> is a boolean,
   * while <code>defaultValue="\"true\""</code> is a string.
   *
   * <p>You <b>must</b> put string values under escaped quotes in order to make it a valid json
   * string.
   */
  String defaultValue() default "";
}
