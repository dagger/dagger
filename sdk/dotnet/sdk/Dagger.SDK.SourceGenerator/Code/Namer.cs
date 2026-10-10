using System.Collections.Generic;
using System.Text.Json;

namespace Dagger.SDK.SourceGenerator.Code;

/// <summary>
/// Names C# members after schema names.
///
/// With names the engine formatted (a names.json next to introspection.json,
/// as <c>codegen introspect --names-out</c> writes it for schemas from
/// v1.0.0), names follow the .NET Framework Design Guidelines: methods and
/// properties PascalCase, parameters camelCase, acronyms written like words
/// (WithGpu, InsecureSkipTlsVerify). For a name the file doesn't have, or
/// without the file, the legacy conversion in <see cref="Formatter"/>
/// applies, so older schemas generate the same code as before.
///
/// Only C# identifiers change: selected fields, argument names, input object
/// fields, enum values and GraphQL type names stay as the schema has them.
/// </summary>
public class Namer(
    IReadOnlyDictionary<string, string>? pascal,
    IReadOnlyDictionary<string, string>? camel
)
{
    /// <summary>
    /// The names file format of methods and properties.
    /// </summary>
    public const string PascalFormat = "PASCAL:CAPITALIZED";

    /// <summary>
    /// The names file format of parameters.
    /// </summary>
    public const string CamelFormat = "CAMEL:CAPITALIZED";

    /// <summary>
    /// The formats the source generator reads from a names file.
    /// </summary>
    public static readonly string[] Formats = [PascalFormat, CamelFormat];

    /// <summary>
    /// A namer without formatted names: the legacy conversion for every name.
    /// </summary>
    public static readonly Namer Legacy = new(null, null);

    /// <summary>
    /// A namer for the formatted names of a names file, which maps each
    /// CASING:ACRONYMS format to every schema name formatted in it.
    /// </summary>
    public static Namer FromNames(IReadOnlyDictionary<string, Dictionary<string, string>>? names)
    {
        if (names == null)
        {
            return Legacy;
        }
        names.TryGetValue(PascalFormat, out var pascal);
        names.TryGetValue(CamelFormat, out var camel);
        return new Namer(pascal, camel);
    }

    /// <summary>
    /// Parses a names file.
    /// </summary>
    public static Dictionary<string, Dictionary<string, string>> ParseNames(string json) =>
        JsonSerializer.Deserialize<Dictionary<string, Dictionary<string, string>>>(json)
        ?? throw new JsonException("names file is null");

    /// <summary>
    /// The name of the method for a field.
    /// </summary>
    public string Method(string name) => Lookup(pascal, name) ?? Formatter.FormatMethod(name);

    /// <summary>
    /// The name of the property for an input object field.
    /// </summary>
    public string Property(string name) => Lookup(pascal, name) ?? Formatter.FormatProperty(name);

    /// <summary>
    /// The name of the parameter for an argument or input object field.
    /// </summary>
    public string Var(string name) => Formatter.FormatVarName(Lookup(camel, name) ?? name);

    private static string? Lookup(IReadOnlyDictionary<string, string>? names, string name) =>
        names != null && names.TryGetValue(name, out var formatted) && formatted.Length > 0
            ? formatted
            : null;
}
