package io.dagger.module.annotation;

import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * Mark an object as a collection: a set of items, each with a unique key.
 *
 * <p>Callers do not see the collection's keys field and get function. The engine serves {@code
 * keys}, {@code get}, {@code list}, and {@code subset} on it instead, beside its other functions. A
 * collection has a keys field, marked with {@link Keys}, and a get function, marked with {@link
 * Get}. Without the markers, the engine looks for members named {@code keys} and {@code get}.
 *
 * <pre>{@code
 * @Object
 * @Collection
 * public class Items {
 *   @Keys private List<String> names;
 *
 *   @Function
 *   @Get
 *   public Item lookup(String name) {
 *     return new Item(name);
 *   }
 * }
 * }</pre>
 *
 * <p><strong>Important:</strong> This annotation must be combined with {@link Object}.
 */
@Target({ElementType.TYPE})
@Retention(RetentionPolicy.SOURCE)
public @interface Collection {}
