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
/// <item><description>Properties (fields) - to customize field names in the API</description></item>
/// <item><description>Methods (functions) - to customize function names in the API</description></item>
/// </list>
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
///     // Custom field name in API
///     [Name("customFieldName")]
///     public string InternalFieldName { get; set; }
///
///     // Custom function name in API
///     [Function]
///     [Name("import")]
///     public string Import_([Name("from")] string from_)
///     {
///         return from_;
///     }
/// }
/// </code>
/// </example>
[AttributeUsage(
    AttributeTargets.Parameter | AttributeTargets.Property | AttributeTargets.Method,
    AllowMultiple = false,
    Inherited = false
)]
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
