using System.Collections.Generic;
using Dagger.SDK.SourceGenerator.Types;

namespace Dagger.SDK.SourceGenerator.Code;

/// <summary>
/// Names C# members after schema names.
///
/// With the words the engine writes to the schema JSON's __identifiers
/// (engines from v1.0.0), names follow the .NET Framework Design Guidelines:
/// methods and properties PascalCase, parameters camelCase, acronyms written
/// like words (WithGpu, InsecureSkipTlsVerify). Without words for a name,
/// the legacy conversion in <see cref="Formatter"/> applies, so older schemas
/// generate the same code as before.
///
/// Only C# identifiers change: selected fields, argument names, input object
/// fields, enum values and GraphQL type names stay as the schema has them.
/// </summary>
public class Namer(IReadOnlyDictionary<string, IdentifierWord[]>? identifiers)
{
    /// <summary>
    /// A namer without words: the legacy conversion for every name.
    /// </summary>
    public static readonly Namer Legacy = new(null);

    /// <summary>
    /// The name of the method for a field.
    /// </summary>
    public string Method(string name) =>
        Format(name, Casing.Pascal) ?? Formatter.FormatMethod(name);

    /// <summary>
    /// The name of the property for an input object field.
    /// </summary>
    public string Property(string name) =>
        Format(name, Casing.Pascal) ?? Formatter.FormatProperty(name);

    /// <summary>
    /// The name of the parameter for an argument or input object field.
    /// </summary>
    public string Var(string name) => Formatter.FormatVarName(Format(name, Casing.Camel) ?? name);

    private string? Format(string name, Casing casing)
    {
        if (
            identifiers == null
            || !identifiers.TryGetValue(name, out var words)
            || !IdentifierFormatter.CanFormat(words)
        )
        {
            return null;
        }
        return IdentifierFormatter.Format(words, casing, AcronymStyle.Capitalized);
    }
}
