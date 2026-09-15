package io.dagger.module.annotation;

import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * Mark a function as a check.
 *
 * <p>Checks are validation functions that return void or throw exceptions to indicate pass/fail
 * status. They are used to validate conditions and can be executed as part of a module's test
 * suite.
 *
 * <p>Example usage:
 *
 * <pre>{@code
 * @Object
 * public class MyModule {
 *   @Function
 *   @Check
 *   public void validateBuild() {
 *     // Validation logic here
 *     // Throw exception on failure
 *   }
 * }
 * }</pre>
 *
 * <p><strong>Important:</strong> This annotation must be combined with {@link Function} annotation.
 * Checks must be callable functions that can be invoked via {@code dagger check}.
 */
@Target({ElementType.METHOD})
@Retention(RetentionPolicy.SOURCE)
public @interface Check {}
