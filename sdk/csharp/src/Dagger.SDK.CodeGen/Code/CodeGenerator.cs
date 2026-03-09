using System;
using System.Linq;
using System.Text;
using Dagger.SDK.CodeGen.Types;
using Type = Dagger.SDK.CodeGen.Types.Type;

namespace Dagger.SDK.CodeGen.Code;

public class CodeGenerator(ICodeRenderer renderer)
{
    private readonly string[] _primitiveTypes = ["ID", "String", "Int", "Float", "Boolean"];

    public string Generate(Introspection introspection)
    {
        var builder = new StringBuilder(renderer.RenderPre());

        builder.AppendLine();

        var distinctTypes = introspection
            .Schema.Types.ExceptBy(_primitiveTypes, type => type.Name)
            .Where(NotInternalTypes)
            .GroupBy(t => t.Name)
            .Select(g => g.First())
            .ToArray(); // Materialize to allow multiple iterations

        // Wire up parent object references for all fields
        foreach (var type in distinctTypes)
        {
            if (type.Fields != null)
            {
                foreach (var field in type.Fields)
                {
                    field.ParentType = type;
                }
            }
        }

        _ = distinctTypes
            .Select(Render)
            .Aggregate(builder, (b, code) => b.Append(code).AppendLine());

        return renderer.Format(builder.ToString());
    }

    private bool NotInternalTypes(Type type) => !type.Name.StartsWith('_');

    private string Render(Type type)
    {
        return type.Kind switch
        {
            TypeKind.OBJECT => renderer.RenderObject(type),
            TypeKind.SCALAR => renderer.RenderScalar(type),
            TypeKind.INPUT_OBJECT => renderer.RenderInputObject(type),
            TypeKind.ENUM => renderer.RenderEnum(type),
            TypeKind.INTERFACE => renderer.RenderInterface(type),
            _ => throw new Exception($"Type kind {type.Kind} is not supported"),
        };
    }
}
