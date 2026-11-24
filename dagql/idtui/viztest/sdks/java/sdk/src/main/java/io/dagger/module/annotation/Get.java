package io.dagger.module.annotation;

import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * Mark the function of a {@link Collection} that returns the item for a key.
 *
 * <p>The function takes one argument of the key type and returns an object.
 *
 * <p><strong>Important:</strong> This annotation must be combined with {@link Function}.
 */
@Target({ElementType.METHOD})
@Retention(RetentionPolicy.SOURCE)
public @interface Get {}
