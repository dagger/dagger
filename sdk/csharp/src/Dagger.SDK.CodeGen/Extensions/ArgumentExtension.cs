using Dagger.SDK.CodeGen.Code;
using Dagger.SDK.CodeGen.Types;

namespace Dagger.SDK.CodeGen.Extensions;

public static class ArgumentExtension
{
    extension(InputValue arg)
    {
        /// <summary>
        /// Converts the argument name into a C# variable name.
        /// </summary>
        public string GetVarName() => Formatter.FormatVarName(arg.Name);
    }
}
