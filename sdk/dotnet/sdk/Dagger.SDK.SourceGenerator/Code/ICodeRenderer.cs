using Dagger.SDK.SourceGenerator.Types;

namespace Dagger.SDK.SourceGenerator.Code;

public interface ICodeRenderer
{
    string RenderPre();

    string RenderObject(Type type);

    string RenderEnum(Type type);

    string RenderScalar(Type type);

    string RenderInputObject(Type type);

    string Format(string source);
}
