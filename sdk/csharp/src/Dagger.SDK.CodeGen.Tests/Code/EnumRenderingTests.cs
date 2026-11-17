using Dagger.SDK.CodeGen.Code;
using Dagger.SDK.CodeGen.Types;

namespace Dagger.SDK.CodeGen.Tests.Code;

[TestClass]
public class EnumRenderingTests
{
    [TestMethod]
    public void RenderEnum_JsonConverterTypeMatchesFormattedEnumName()
    {
        var type = new Types.Type
        {
            Name = "LLMContentBlockKind",
            Description = "The kind of content in a message block.",
            Kind = TypeKind.ENUM,
            EnumValues =
            [
                new EnumValue { Name = "TEXT", Description = "Plain text content." },
                new EnumValue { Name = "TOOL_CALL", Description = "A tool/function call." },
            ],
        };

        var rendered = new CodeRenderer().RenderEnum(type);
        var formatted = Formatter.FormatType(type.Name);

        StringAssert.Contains(rendered, $"public enum {formatted}");
        StringAssert.Contains(
            rendered,
            $"JsonStringEnumConverter<{formatted}>",
            "JsonConverter must use the formatted C# enum name, not the raw GraphQL name."
        );
        Assert.IsFalse(
            rendered.Contains($"JsonStringEnumConverter<{type.Name}>") && formatted != type.Name,
            "JsonConverter must not keep the raw GraphQL acronym name when FormatType rewrites it."
        );
    }
}
