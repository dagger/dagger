namespace Dagger;

/// <summary>
/// Specifies a custom name to use when exposing this element to the Dagger API.
/// This is useful for avoiding C# keyword conflicts or providing more idiomatic API names.
/// </summary>
/// <remarks>
/// Can be applied to:
/// <list type="bullet">
/// <item><description>Constructor parameters - to customize parameter names in the API</description></item>
/// <item><description>Function parameters - to customize parameter names in the API</description></item>
/// </list>
/// <para>
/// Note: To customize function or field names, use <c>[Function(Name = "...")]</c> instead.
/// The [Name] attribute is only for parameters, following the pattern used in other Dagger SDKs.
/// </para>
/// </remarks>
/// <example>
/// <code>
/// [Object]
/// public class MyModule
/// {
///     // Avoid C# keyword "from" by using Name attribute
///     public MyModule([Name("from")] string from_)
///     {
///         _source = from_;
///     }
///
///     // Custom field name in API - use Function(Name)
///     [Function(Name = "customFieldName")]
///     public string InternalFieldName { get; set; }
///
///     // Custom function name in API - use Function(Name) not [Name]
///     [Function(Name = "import")]
///     public string Import_([Name("from")] string from_)
///     {
///         return from_;
///     }
/// }
/// </code>
/// </example>
[AttributeUsage(AttributeTargets.Parameter, AllowMultiple = false, Inherited = false)]
public sealed class NameAttribute : Attribute
{
    /// <summary>
    /// Gets the custom name to use in the Dagger API.
    /// </summary>
    public string ApiName { get; }

    /// <summary>
    /// Initializes a new instance of the <see cref="NameAttribute"/> class.
    /// </summary>
    /// <param name="apiName">The custom name to use in the Dagger API.</param>
    /// <exception cref="ArgumentException">Thrown when apiName is null or whitespace.</exception>
    public NameAttribute(string apiName)
    {
        if (string.IsNullOrWhiteSpace(apiName))
        {
            throw new ArgumentException("API name cannot be null or whitespace.", nameof(apiName));
        }

        ApiName = apiName;
    }
}
