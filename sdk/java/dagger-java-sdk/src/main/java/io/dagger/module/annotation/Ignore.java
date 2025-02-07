package io.dagger.module.annotation;

import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * Patterns to ignore when loading the directory.
 *
 * <p>You can inverse the behavior by adding a <code>!</code> at the start.
 *
 * <p>For instance if you want to ignore all files except <code>README.md</code> define the
 * following annotation: <code>@Ignore({"**", "!README.md"})</code>
 */
@Target(ElementType.PARAMETER)
@Retention(RetentionPolicy.SOURCE)
public @interface Ignore {
  String[] value();
}
