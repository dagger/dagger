using System.Text.Json.Serialization;

namespace Dagger.SDK.CodeGen.Types;

/// <summary>
/// Represents an argument passed to a GraphQL directive.
/// </summary>
/// <remarks>
/// Directive arguments provide additional metadata for the directive.
/// For example, @deprecated(reason: "Use X instead") has one argument named "reason".
/// </remarks>
public sealed class DirectiveArg
{
    /// <summary>
    /// The name of the directive argument.
    /// </summary>
    [JsonPropertyName("name")]
    public required string Name { get; set; }

    /// <summary>
    /// The value of the directive argument.
    /// </summary>
    /// <remarks>
    /// This is stored as JsonElement to handle various value types (string, int, bool, etc.).
    /// Use extension methods to access strongly-typed values.
    /// </remarks>
    [JsonPropertyName("value")]
    public required System.Text.Json.JsonElement Value { get; set; }
}
