using Dagger.SDK.CodeGen.Code;
using Dagger.SDK.CodeGen.Types;

namespace Dagger.SDK.CodeGen.Extensions;

public static class TypeRefExtension
{
    extension(TypeRef typeRef)
    {
        /// <summary>
        /// Gets the type name from a TypeRef.
        ///
        /// This method doesn't indicate whether the type is nullable. The caller
        /// must detect nullability from the TypeRef object themselves.
        /// </summary>
        public string GetTypeName()
        {
            var tr = typeRef.GetType_();
            if (tr.IsList())
            {
                return $"{tr.OfType.GetTypeName()}[]";
            }
            return Formatter.FormatType(tr.Name);
        }
    }
}
