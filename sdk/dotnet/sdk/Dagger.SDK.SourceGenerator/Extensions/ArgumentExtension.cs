using Dagger.SDK.SourceGenerator.Code;
using Dagger.SDK.SourceGenerator.Types;

namespace Dagger.SDK.SourceGenerator.Extensions;

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
