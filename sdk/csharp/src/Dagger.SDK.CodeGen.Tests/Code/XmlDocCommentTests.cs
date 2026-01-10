using Dagger.SDK.CodeGen.Code;
using Dagger.SDK.CodeGen.Types;

namespace Dagger.SDK.CodeGen.Tests.Code;

[TestClass]
public class XmlDocCommentTests
{
    [DataTestMethod]
    [DataRow("Simple description", "Simple description")]
    [DataRow("Description with <angle> brackets", "Description with &lt;angle&gt; brackets")]
    [DataRow("Generic type Task<Container>", "Generic type Task&lt;Container&gt;")]
    [DataRow(
        "Multiple <types> like List<string>",
        "Multiple &lt;types&gt; like List&lt;string&gt;"
    )]
    [DataRow("Ampersand & character", "Ampersand &amp; character")]
    [DataRow("Quote \" character", "Quote &quot; character")]
    [DataRow("Already escaped &lt;text&gt;", "Already escaped &amp;lt;text&amp;gt;")]
    public void RenderSummaryDocComment_EscapesXmlSpecialCharacters(
        string input,
        string expectedEscaped
    )
    {
        // We need to test the private method indirectly through generated code
        // Let's create a simple type with the description and verify the output
        var type = new Types.Type
        {
            Name = "TestType",
            Description = input,
            Kind = TypeKind.OBJECT,
            Fields = Array.Empty<Field>(),
        };

        var renderer = new CodeRenderer();
        var result = renderer.RenderObject(type);

        // The result should contain the escaped version in the XML doc comment
        Assert.IsTrue(
            result.Contains($"/// {expectedEscaped}"),
            $"Expected to find '/// {expectedEscaped}' in generated code"
        );
    }

    [TestMethod]
    public void ParameterDescription_EscapesXmlSpecialCharacters()
    {
        // Test that parameter descriptions also escape XML characters
        var field = new Field
        {
            Name = "testMethod",
            Description = "A test method",
            DeprecationReason = string.Empty,
            Args =
            [
                new InputValue
                {
                    Name = "param1",
                    Description = "Parameter with <generic> type Task<T>",
                    Type = new TypeRef { Kind = TypeKind.SCALAR, Name = "String" },
                },
            ],
            Type = new TypeRef { Kind = TypeKind.SCALAR, Name = "String" },
        };

        var type = new Types.Type
        {
            Name = "TestType",
            Description = "Test",
            Kind = TypeKind.OBJECT,
            Fields = [field],
        };

        // Wire up parent reference
        field.ParentType = type;

        var renderer = new CodeRenderer();
        var result = renderer.RenderObject(type);

        // Parameter description should be escaped
        Assert.IsTrue(result.Contains("Parameter with &lt;generic&gt; type Task&lt;T&gt;"));
    }

    [TestMethod]
    public void MultilineDescription_EscapesAllLines()
    {
        var type = new Types.Type
        {
            Name = "TestType",
            Description = "Line 1 with <brackets>\nLine 2 with Task<Container>\nLine 3 normal",
            Kind = TypeKind.OBJECT,
            Fields = Array.Empty<Field>(),
        };

        var renderer = new CodeRenderer();
        var result = renderer.RenderObject(type);

        // All lines should be escaped
        Assert.IsTrue(result.Contains("Line 1 with &lt;brackets&gt;"));
        Assert.IsTrue(result.Contains("Line 2 with Task&lt;Container&gt;"));
        Assert.IsTrue(result.Contains("Line 3 normal"));
    }

    [TestMethod]
    public void ParameterName_EscapesXmlSpecialCharacters()
    {
        // Even though parameter names shouldn't normally contain special chars,
        // we should still escape them for safety
        var field = new Field
        {
            Name = "testMethod",
            Description = "A test method",
            DeprecationReason = string.Empty,
            Args =
            [
                new InputValue
                {
                    Name = "param<weird>name", // Unlikely but possible from GraphQL
                    Description = "Test param",
                    Type = new TypeRef { Kind = TypeKind.SCALAR, Name = "String" },
                },
            ],
            Type = new TypeRef { Kind = TypeKind.SCALAR, Name = "String" },
        };

        var type = new Types.Type
        {
            Name = "TestType",
            Description = "Test",
            Kind = TypeKind.OBJECT,
            Fields = [field],
        };

        // Wire up parent reference
        field.ParentType = type;

        var renderer = new CodeRenderer();
        var result = renderer.RenderObject(type);

        // Parameter name in XML attribute should be escaped
        // Note: This tests the param name attribute, not just the description
        Assert.IsTrue(result.Contains("param name=\"param&lt;weird&gt;name\""));
    }
}
