using System.Linq;
using Dagger.SDK.SourceGenerator.Extensions;

namespace Dagger.SDK.SourceGenerator.Code;

public static class Formatter
{
    private static readonly string[] Keywords =
    [
        "abstract",
        "as",
        "base",
        "bool",
        "break",
        "byte",
        "case",
        "catch",
        "char",
        "checked",
        "class",
        "const",
        "continue",
        "decimal",
        "default",
        "delegate",
        "do",
        "double",
        "else",
        "enum",
        "event",
        "explicit",
        "extern",
        "false",
        "finally",
        "fixed",
        "float",
        "for",
        "foreach",
        "goto",
        "if",
        "implicit",
        "in",
        "int",
        "interface",
        "internal",
        "is",
        "lock",
        "long",
        "namespace",
        "new",
        "null",
        "object",
        "operator",
        "out",
        "override",
        "params",
        "private",
        "protected",
        "public",
        "readonly",
        "ref",
        "return",
        "sbyte",
        "sealed",
        "short",
        "sizeof",
        "stackalloc",
        "static",
        "string",
        "struct",
        "switch",
        "this",
        "throw",
        "true",
        "try",
        "typeof",
        "uint",
        "ulong",
        "unchecked",
        "unsafe",
        "ushort",
        "using",
        "virtual",
        "void",
        "volatile",
        "while",
    ];

    public static string FormatMethod(string name) => name.ToPascalCase();

    public static string FormatProperty(string name) => name.ToPascalCase();

    public static string FormatVarName(string name) => Keywords.Contains(name) ? $"{name}_" : name;

    public static string FormatType(string typeName)
    {
        return typeName switch
        {
            "String" => "string",
            "Boolean" => "bool",
            "Int" => "int",
            "Float" => "float",
            _ => typeName.ToPascalCase(),
        };
    }
}
