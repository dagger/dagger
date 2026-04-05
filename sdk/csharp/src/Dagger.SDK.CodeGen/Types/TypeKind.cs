using System.Text.Json.Serialization;

namespace Dagger.SDK.CodeGen.Types;

[JsonConverter(typeof(JsonStringEnumConverter))]
public enum TypeKind
{
    SCALAR,
    OBJECT,
    INTERFACE,
    UNION,
    ENUM,
    INPUT_OBJECT,
    LIST,
    NON_NULL,
}
