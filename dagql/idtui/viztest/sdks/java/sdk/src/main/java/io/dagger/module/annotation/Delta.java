package io.dagger.module.annotation;

import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * Mark the {@code CollectionDelta} field of a {@link Collection} that receives the keys added and
 * removed since the module returned the collection.
 *
 * <p>The engine fills the field before each call. It is part of the object's state whatever its
 * visibility.
 */
@Target({ElementType.FIELD})
@Retention(RetentionPolicy.SOURCE)
public @interface Delta {}
