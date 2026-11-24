using Dagger.SDK.SourceGenerator.Extensions;
using Microsoft.CodeAnalysis.CSharp;

namespace Dagger.SDK.SourceGenerator.Code;

public static class Formatter
{
    public static string FormatMethod(string name) => name.ToPascalCase();

    public static string FormatProperty(string name) => name.ToPascalCase();

    public static string FormatVarName(string name) =>
        SyntaxFacts.GetKeywordKind(name) != SyntaxKind.None ? $"{name}_" : name;

    public static string FormatType(string typeName)
    {
        return typeName switch
        {
            "String" => SyntaxFacts.GetText(SyntaxKind.StringKeyword),
            "Boolean" => SyntaxFacts.GetText(SyntaxKind.BoolKeyword),
            "Int" => SyntaxFacts.GetText(SyntaxKind.IntKeyword),
            "Float" => SyntaxFacts.GetText(SyntaxKind.FloatKeyword),
            "ID" => "Id",
            _ => typeName,
        };
    }

    /// <summary>
    /// Whether a GraphQL type is a built-in scalar, with a fixed C# type.
    /// </summary>
    public static bool IsBuiltinType(string typeName) =>
        typeName is "String" or "Boolean" or "Int" or "Float" or "ID";
}
