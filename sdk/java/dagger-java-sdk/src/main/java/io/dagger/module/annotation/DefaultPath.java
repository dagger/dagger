package io.dagger.module.annotation;

import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * Default load path
 *
 * <p>This applies to io.dagger.client.Directory or io.dagger.client.File types.
 *
 * <p>Path is relative to root directory.
 */
@Target(ElementType.PARAMETER)
@Retention(RetentionPolicy.SOURCE)
public @interface DefaultPath {
  String value() default "";
}
