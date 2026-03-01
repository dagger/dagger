using System.Text.Json.Serialization;

namespace Dagger.SDK.CodeGen.Types;

/// <summary>
/// Represents a GraphQL directive attached to a schema element.
/// </summary>
/// <remarks>
/// Directives provide metadata annotations on types, fields, arguments, and other schema elements.
/// Common directives include @deprecated and @experimental, but custom directives may also be present.
/// This class stores all directives generically to support future extensibility.
/// </remarks>
public sealed class Directive
{
    /// <summary>
    /// The name of the directive (without the @ symbol).
    /// </summary>
    [JsonPropertyName("name")]
    public required string Name { get; set; }

    /// <summary>
    /// The arguments passed to the directive, if any.
    /// </summary>
    [JsonPropertyName("args")]
    public DirectiveArg[]? Args { get; set; }
}
