using System;

namespace Dagger;

/// <summary>
/// Marks a function as a generator for <c>dagger generate</c>.
/// Generators return a <see cref="Changeset"/> describing the files to change and
/// must be callable without arguments (all parameters optional or contextual).
///
/// Supported return types: <c>Changeset</c> or <c>Task&lt;Changeset&gt;</c>.
/// </summary>
/// <example>
/// <code>
/// [Object]
/// public class MyModule
/// {
///     [Function]
///     [Generate]
///     public Changeset Codegen([DefaultPath("/")] Directory source)
///     {
///         var generated = source.WithNewFile("gen.txt", "generated");
///         return generated.Changes(source);
///     }
/// }
/// </code>
/// </example>
[AttributeUsage(AttributeTargets.Method, AllowMultiple = false, Inherited = false)]
public class GenerateAttribute : Attribute { }
