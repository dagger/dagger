package io.dagger.module.annotation;

import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * Indicate a parameter can be a null value.
 *
 * <p>Please prefer the usage of <code>@Default("null")</code> if you want to indicate the parameter
 * to be null and make it optional for the user to specify.
 *
 * <p>Otherwise use this annotation in addition to <code>@Default</code> like in the following
 * <code>@Nullable @Default("Foo")</code>. But be sure to understand that you will not be able to
 * provide a <code>null</code> value to the parameter.
 *
 * <p>If not present, a check will be performed to ensure a function is not called with a null
 * value.
 */
@Target(ElementType.PARAMETER)
@Retention(RetentionPolicy.SOURCE)
public @interface Nullable {}
