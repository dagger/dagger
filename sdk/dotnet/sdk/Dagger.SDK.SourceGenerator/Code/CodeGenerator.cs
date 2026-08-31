using System;
using System.Collections.Generic;
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
        // Collect type name sets for the renderer
        if (renderer is CodeRenderer codeRenderer)
        {
            codeRenderer.SupportsNullableObjects = SupportsNullableObjects(
                introspection.SchemaVersion
            );
            codeRenderer.ObjectTypeNames = new HashSet<string>(
                introspection.Schema.Types.Where(t => t.Kind == "OBJECT").Select(t => t.Name)
            );
            codeRenderer.InterfaceTypes = introspection
                .Schema.Types.Where(t => t.Kind == "INTERFACE")
                .ToDictionary(t => t.Name);
        }

        var builder = new StringBuilder(renderer.RenderPre());

        builder.AppendLine();

        _ = introspection
            .Schema.Types.ExceptBy(_primitiveTypes, type => type.Name)
            .Where(NotInternalTypes)
            .Select(Render)
            .Aggregate(builder, (b, code) => b.Append(code).AppendLine());

        return renderer.Format(builder.ToString());
    }

    private static bool SupportsNullableObjects(string? schemaVersion)
    {
        if (string.IsNullOrWhiteSpace(schemaVersion))
        {
            return true;
        }

        var version = schemaVersion!.TrimStart('v');
        var parts = version.Split(new[] { '-' }, 2);
        if (!Version.TryParse(parts[0], out var core))
        {
            return true;
        }

        var cutover = new Version(1, 0, 0);
        if (core != cutover)
        {
            return core > cutover;
        }
        if (parts.Length == 1)
        {
            return true;
        }

        const string betaPrefix = "beta.";
        return
            parts[1].StartsWith(betaPrefix)
            && int.TryParse(parts[1].Substring(betaPrefix.Length).Split('-', '+')[0], out var beta)
            ? beta >= 10
            : string.CompareOrdinal(parts[1], "beta") > 0;
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
            "INTERFACE" => renderer.RenderInterface(type),
            _ => throw new Exception($"Type kind {type.Kind} is not supported"),
        };
    }
}
