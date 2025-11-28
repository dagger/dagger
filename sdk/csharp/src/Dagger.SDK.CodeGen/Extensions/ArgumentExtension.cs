using Dagger.SDK.CodeGen.Code;
using Dagger.SDK.CodeGen.Types;

namespace Dagger.SDK.CodeGen.Extensions;

public static class ArgumentExtension
{
    // <summary>
    // Convert argument name into C# variable name.
    // </summary>
    public static string GetVarName(this InputValue arg)
    {
        return Formatter.FormatVarName(arg.Name);
    }
}