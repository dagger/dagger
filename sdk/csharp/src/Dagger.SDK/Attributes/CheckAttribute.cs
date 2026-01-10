using System;

namespace Dagger;

/// <summary>
/// Marks a function as a check. Checks are validation/test functions that validate conditions.
/// Check functions must not have required parameters.
///
/// Supported return types:
/// - void: Throws exception on failure
/// - Task: Async validation, throws exception on failure
/// - Container: Executes container and checks exit code (non-zero = failure)
/// - Task&lt;Container&gt;: Async container execution
/// </summary>
/// <example>
/// <code>
/// [Object]
/// public class MyModule
/// {
///     // Synchronous check with void return
///     [Function]
///     [Check]
///     public void Lint()
///     {
///         if (hasErrors) throw new Exception("Lint failed");
///     }
///
///     // Container-based check (validated by exit code)
///     [Function]
///     [Check]
///     public Container TestContainer()
///     {
///         return Dag.Container()
///             .From("alpine:3")
///             .WithExec(["sh", "-c", "exit 0"]);
///     }
/// }
/// </code>
/// </example>
[AttributeUsage(AttributeTargets.Method, AllowMultiple = false, Inherited = false)]
public class CheckAttribute : Attribute { }
