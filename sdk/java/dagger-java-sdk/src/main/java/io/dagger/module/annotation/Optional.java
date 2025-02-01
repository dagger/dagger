package io.dagger.module.annotation;

import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

@Target(ElementType.PARAMETER)
@Retention(RetentionPolicy.SOURCE)
public @interface Optional {
  /**
   * Default value of the parameter. This should be a <b>valid JSON string</b>.
   *
   * <p>This means, be default, a string should be between quotes like <code>
   * defaultValue="\"string value\""</code>.
   *
   * <p>This helps to distinguish it from other types like boolean or integer, for instance between
   * <code>defaultValue="true"</code> a boolean value and <code>defaultValue="\"true\""</code> a
   * string value.
   *
   * <p>That said to make it easier to work with, if the type of the argument is a java.lang.String,
   * and the default value is not between quotes, quotes will be added and all double quotes inside
   * the value will be escaped
   *
   * <p>For instance <code>defaultValue="this \" is a quote"</code> will be automatically converted
   * to <code>defaultValue="\"this is \\\" a quote \""</code>
   */
  String defaultValue() default "";
}
