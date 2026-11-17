using System.Reflection;
using Dagger.ModuleRuntime;

namespace Dagger;

/// <summary>
/// Conversions from Dagger objects to module-declared interfaces.
/// </summary>
public static class InterfaceConversionExtensions
{
    /// <summary>
    /// Treats this object as an implementation of the module interface
    /// <typeparamref name="T"/>. Use this to return or pass an object from
    /// another module where one of your own [Interface] types is expected,
    /// e.g. <c>Dag.Mallard().AsInterface&lt;IDuck&gt;()</c>.
    ///
    /// The conversion is engine-checked at call time: the object's underlying
    /// type must actually provide the interface's functions.
    /// </summary>
    /// <typeparam name="T">A module interface type decorated with [Interface].</typeparam>
    /// <param name="source">The object to convert.</param>
    public static T AsInterface<T>(this IId<Id> source)
        where T : class
    {
        if (typeof(T).GetCustomAttribute<InterfaceAttribute>() is null)
        {
            throw new ArgumentException(
                $"Type '{typeof(T).Name}' is not a Dagger interface ([Interface] attribute missing)."
            );
        }

        var interfaceName = Entrypoint.ResolveInterfaceGraphQLName(typeof(T));
        // The module runtime is a console app without a synchronization
        // context, so blocking on the id resolution here is safe.
        var id = source.Id().GetAwaiter().GetResult();
        return DaggerInterfaceProxy<T>.Create(Client.Dag, interfaceName, id.Value);
    }
}
