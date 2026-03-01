using Dagger.SDK.CodeGen.Types;

namespace Dagger.SDK.CodeGen.Code;

public interface ICodeRenderer
{
    string RenderPre();

    string RenderObject(Type type);

    string RenderEnum(Type type);

    string RenderScalar(Type type);

    string RenderInputObject(Type type);

    string RenderInterface(Type type);

    string Format(string source);
}
