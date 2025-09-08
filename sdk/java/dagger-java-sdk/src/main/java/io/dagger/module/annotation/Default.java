package io.dagger.module.annotation;

import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * Default value of the parameter. This should be a <b>valid JSON string</b>.
 *
 * <p>This means, by default, a string should be between quotes like <code>
 * value="\"string value\""</code>.
 *
 * <p>This helps to distinguish it from other types like boolean or integer, for instance between
 * <code>value="true"</code> a boolean value and <code>value="\"true\""</code> a string value.
 *
 * <p>That said to make it easier to work with, if the type of the argument is a java.lang.String,
 * and the default value is not between quotes, quotes will be added and all double quotes inside
 * the value will be escaped
 *
 * <p>For instance <code>value="this \" is a quote"</code> will be automatically converted to <code>
 * value="\"this is \\\" a quote \""</code>
 *
 * <p><u>One exception exists:</u> if <code>value="null"</code> no quotes will be added to allow to
 * set the json <code>null</code> value. In this very specific case, the parameter will be flagged
 * as <b>nullable</b>.
 *
 * <p>Once a default value is set, the parameter is optional in such that the user doesn't need to
 * provide a value.
 */
@Target(ElementType.PARAMETER)
@Retention(RetentionPolicy.SOURCE)
public @interface Default {
  String value() default "";
}
