using System;

namespace Dagger;

/// <summary>
/// Marks a function as a check: a validation function (test, lint, scan) that
/// <c>dagger check</c> discovers and runs. Check functions must not have
/// required parameters.
///
/// A check returns <c>void</c> or <see cref="System.Threading.Tasks.Task"/> and
/// fails when it throws. To run a container as a check, await its
/// <c>Sync()</c>: a non-zero exit throws, which fails the check.
/// </summary>
/// <example>
/// <code>
/// [Object]
/// public class MyModule
/// {
///     // Synchronous check: fails by throwing
///     [Function]
///     [Check]
///     public void Lint()
///     {
///         if (hasErrors) throw new Exception("Lint failed");
///     }
///
///     // Container check: a non-zero exit makes Sync() throw
///     [Function]
///     [Check]
///     public async Task Test()
///     {
///         await Dag.Container()
///             .From("alpine:3")
///             .WithExec(["sh", "-c", "exit 0"])
///             .Sync();
///     }
/// }
/// </code>
/// </example>
[AttributeUsage(AttributeTargets.Method, AllowMultiple = false, Inherited = false)]
public class CheckAttribute : Attribute { }
