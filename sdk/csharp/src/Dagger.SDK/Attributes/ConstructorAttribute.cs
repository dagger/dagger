namespace Dagger;

/// <summary>
/// Marks a static method as an alternative constructor for a Dagger module.
/// The method must be static and return an instance of the containing class.
/// Supports async methods returning Task&lt;T&gt; for asynchronous initialization.
/// </summary>
/// <remarks>
/// <para>
/// Dagger modules have only one constructor. Use this attribute to specify a static
/// factory method as the constructor instead of using an instance constructor.
/// This is particularly useful for:
/// </para>
/// <list type="bullet">
/// <item><description>Asynchronous initialization (fetching secrets, configs, etc.)</description></item>
/// <item><description>Factory patterns with validation or complex setup logic</description></item>
/// <item><description>Alternative construction methods (FromString, FromConfig, etc.)</description></item>
/// </list>
/// <example>
/// <code>
/// [Object]
/// public class MyModule
/// {
///     private readonly string _apiKey;
///
///     private MyModule(string apiKey)
///     {
///         _apiKey = apiKey;
///     }
///
///     [Constructor]
///     public static async Task&lt;MyModule&gt; CreateAsync(string apiKey)
///     {
///         var validated = await ValidateApiKeyAsync(apiKey);
///         return new MyModule(validated);
///     }
/// }
/// </code>
/// </example>
/// </remarks>
[AttributeUsage(AttributeTargets.Method, AllowMultiple = false, Inherited = false)]
public class ConstructorAttribute : Attribute
{
    /// <summary>
    /// Gets or sets the description of the constructor.
    /// </summary>
    public string? Description { get; set; }

    /// <summary>
    /// Gets or sets the deprecation message if the constructor is deprecated.
    /// </summary>
    public string? Deprecated { get; set; }
}
