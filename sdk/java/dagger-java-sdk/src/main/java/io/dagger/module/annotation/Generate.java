package io.dagger.module.annotation;

import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * Mark a function as a generator.
 *
 * <p>Generators are functions that return a {@code Changeset} representing changes to be applied to
 * the filesystem. They are used to generate or transform code and can be executed via {@code dagger
 * generate}.
 *
 * <p>Example usage:
 *
 * <pre>{@code
 * @Object
 * public class MyModule {
 *   @Function
 *   @Generate
 *   public Changeset generateCode() {
 *     return dag().directory()
 *         .withNewFile("generated.txt", "content")
 *         .changes(dag().directory());
 *   }
 * }
 * }</pre>
 *
 * <p><strong>Important:</strong> This annotation must be combined with {@link Function} annotation.
 * Generator functions must return a {@code Changeset} and can be invoked via {@code dagger
 * generate}.
 */
@Target({ElementType.METHOD})
@Retention(RetentionPolicy.SOURCE)
public @interface Generate {}
