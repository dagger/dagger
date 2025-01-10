using System;
using System.Linq;
using System.Text;
using Dagger.SDK.SourceGenerator.Extensions;
using Dagger.SDK.SourceGenerator.Types;
using Type = Dagger.SDK.SourceGenerator.Types.Type;

namespace Dagger.SDK.SourceGenerator.Code;

public class CodeGenerator(ICodeRenderer renderer)
{
    private readonly string[] _primitiveTypes = ["ID", "String", "Int", "Float", "Boolean"];

    public string Generate(Introspection introspection)
    {
        var builder = new StringBuilder(renderer.RenderPre());

        builder.AppendLine();

        _ = introspection
            .Schema.Types.ExceptBy(_primitiveTypes, type => type.Name)
            .Where(NotInternalTypes)
            .Select(Render)
            .Aggregate(builder, (b, code) => b.Append(code).AppendLine());

        return renderer.Format(builder.ToString());
    }

    private bool NotInternalTypes(Type type) => !type.Name.StartsWith("_");

    private string Render(Type type)
    {
        return type.Kind switch
        {
            "OBJECT" => renderer.RenderObject(type),
            "SCALAR" => renderer.RenderScalar(type),
            "INPUT_OBJECT" => renderer.RenderInputObject(type),
            "ENUM" => renderer.RenderEnum(type),
            _ => throw new Exception($"Type kind {type.Kind} is not supported"),
        };
    }
}
