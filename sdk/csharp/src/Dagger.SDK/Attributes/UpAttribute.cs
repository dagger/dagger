using System;

namespace Dagger;

/// <summary>
/// Marks a function as a service for <c>dagger up</c>.
/// The function must return a <see cref="Service"/> and must be callable without
/// arguments (all parameters optional or contextual).
///
/// Supported return types: <c>Service</c> or <c>Task&lt;Service&gt;</c>.
/// </summary>
/// <example>
/// <code>
/// [Object]
/// public class MyModule
/// {
///     [Function]
///     [Up]
///     public Service Web()
///     {
///         return Dag.Container()
///             .From("nginx:alpine")
///             .WithExposedPort(80)
///             .AsService();
///     }
/// }
/// </code>
/// </example>
[AttributeUsage(AttributeTargets.Method, AllowMultiple = false, Inherited = false)]
public class UpAttribute : Attribute { }
