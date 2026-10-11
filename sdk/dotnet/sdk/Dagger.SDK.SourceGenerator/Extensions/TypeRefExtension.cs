using Dagger.SDK.SourceGenerator.Code;
using Dagger.SDK.SourceGenerator.Types;

namespace Dagger.SDK.SourceGenerator.Extensions;

public static class TypeRefExtension
{
    // <summary>
    // Get a type from TypeRef.
    //
    // This method doesn't indicate the type is nullable or not. The caller
    // must detecting it from TypeRef object by themself.
    // </summary>
    public static string GetTypeName(this TypeRef typeRef) => typeRef.GetTypeName(Namer.Legacy);

    // <summary>
    // Get a type from TypeRef, named by a Namer.
    // </summary>
    public static string GetTypeName(this TypeRef typeRef, Namer namer)
    {
        var tr = typeRef.GetType_();
        if (tr.IsList())
        {
            return $"{tr.OfType!.GetTypeName(namer)}[]";
        }
        return namer.Type(tr.Name);
    }
}
