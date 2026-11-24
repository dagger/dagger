package io.dagger.module.annotation;

import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * Mark the field of a {@link Collection} that holds one unique key per item.
 *
 * <p>The field is a list of a scalar or an enum, such as {@code List<String>}. It is part of the
 * object's state whatever its visibility.
 */
@Target({ElementType.FIELD})
@Retention(RetentionPolicy.SOURCE)
public @interface Keys {}
