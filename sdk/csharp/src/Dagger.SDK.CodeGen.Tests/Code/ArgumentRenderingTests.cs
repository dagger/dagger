using Dagger.SDK.CodeGen.Code;
using Dagger.SDK.CodeGen.Types;

namespace Dagger.SDK.CodeGen.Tests.Code;

[TestClass]
public class ArgumentRenderingTests
{
    private static TypeRef CreateBooleanTypeRef(bool nullable = true)
    {
        if (nullable)
        {
            return new TypeRef { Kind = TypeKind.SCALAR, Name = "Boolean" };
        }

        return new TypeRef
        {
            Kind = TypeKind.NON_NULL,
            OfType = new TypeRef { Kind = TypeKind.SCALAR, Name = "Boolean" },
        };
    }

    private static TypeRef CreateListTypeRef(string elementType, bool nullable = true)
    {
        var listType = new TypeRef
        {
            Kind = TypeKind.LIST,
            OfType = new TypeRef { Kind = TypeKind.SCALAR, Name = elementType },
        };

        if (nullable)
        {
            return listType;
        }

        return new TypeRef { Kind = TypeKind.NON_NULL, OfType = listType };
    }

    [TestMethod]
    public void RenderOptionalArgument_BoolWithNullDefault_GeneratesNullableBool()
    {
        // Arrange
        var arg = new InputValue
        {
            Name = "enabled",
            Description = "Whether to enable",
            Type = CreateBooleanTypeRef(nullable: true),
            DefaultValue = null,
        };

        // Act
        var result = CodeRenderer.RenderOptionalArgument(arg);

        // Assert
        Assert.IsTrue(
            result.Contains("bool? enabled = null"),
            $"Expected 'bool? enabled = null' but got: {result}"
        );
    }

    [TestMethod]
    public void RenderOptionalArgument_BoolWithFalseDefault_GeneratesNonNullableBool()
    {
        // Arrange
        var arg = new InputValue
        {
            Name = "enabled",
            Description = "Whether to enable",
            Type = CreateBooleanTypeRef(nullable: true),
            DefaultValue = "false",
        };

        // Act
        var result = CodeRenderer.RenderOptionalArgument(arg);

        // Assert
        Assert.IsTrue(
            result.Contains("bool enabled = false"),
            $"Expected 'bool enabled = false' but got: {result}"
        );
        Assert.IsFalse(result.Contains("bool?"), $"Should not contain 'bool?' but got: {result}");
    }

    [TestMethod]
    public void RenderOptionalArgument_BoolWithTrueDefault_GeneratesNonNullableBool()
    {
        // Arrange
        var arg = new InputValue
        {
            Name = "enabled",
            Description = "Whether to enable",
            Type = CreateBooleanTypeRef(nullable: true),
            DefaultValue = "true",
        };

        // Act
        var result = CodeRenderer.RenderOptionalArgument(arg);

        // Assert
        Assert.IsTrue(
            result.Contains("bool enabled = true"),
            $"Expected 'bool enabled = true' but got: {result}"
        );
        Assert.IsFalse(result.Contains("bool?"), $"Should not contain 'bool?' but got: {result}");
    }

    [TestMethod]
    public void RenderOptionalArgument_NonNullBool_StillTreatedAsOptional()
    {
        // Arrange
        var arg = new InputValue
        {
            Name = "enabled",
            Description = "Whether to enable",
            Type = CreateBooleanTypeRef(nullable: false),
            DefaultValue = null,
        };

        // Act - RenderOptionalArgument always adds nullable suffix
        var result = CodeRenderer.RenderOptionalArgument(arg);

        // Assert - even NON_NULL types get nullable suffix in optional context
        Assert.IsTrue(
            result.Contains("bool? enabled = null"),
            $"Expected 'bool? enabled = null' but got: {result}"
        );
    }

    [TestMethod]
    public void RenderArgumentValue_BoolList_GeneratesBooleanValue()
    {
        // Arrange
        var arg = new InputValue
        {
            Name = "boolListArg",
            Description = "A list of booleans",
            Type = CreateListTypeRef("Boolean", nullable: false),
        };

        // Act
        var result = CodeRenderer.RenderArgumentValue(arg);

        // Assert
        Assert.IsTrue(
            result.Contains("new BooleanValue(v)"),
            $"Expected 'new BooleanValue(v)' but got: {result}"
        );
        Assert.IsFalse(
            result.Contains("v.Value"),
            $"Should not contain 'v.Value' but got: {result}"
        );
        Assert.IsTrue(
            result.Contains("new ListValue(boolListArg.Select(v => new BooleanValue(v)"),
            $"Expected proper list value construction but got: {result}"
        );
    }

    [TestMethod]
    public void RenderArgumentValue_StringList_GeneratesStringValue()
    {
        // Arrange
        var arg = new InputValue
        {
            Name = "stringListArg",
            Description = "A list of strings",
            Type = CreateListTypeRef("String", nullable: false),
        };

        // Act
        var result = CodeRenderer.RenderArgumentValue(arg);

        // Assert
        Assert.IsTrue(
            result.Contains("new StringValue(v)"),
            $"Expected 'new StringValue(v)' but got: {result}"
        );
        Assert.IsTrue(
            result.Contains("new ListValue(stringListArg.Select(v => new StringValue(v)"),
            $"Expected proper list value construction but got: {result}"
        );
    }

    [TestMethod]
    public void RenderArgumentValue_IntList_GeneratesIntValue()
    {
        // Arrange
        var arg = new InputValue
        {
            Name = "intListArg",
            Description = "A list of integers",
            Type = CreateListTypeRef("Int", nullable: false),
        };

        // Act
        var result = CodeRenderer.RenderArgumentValue(arg);

        // Assert
        Assert.IsTrue(
            result.Contains("new IntValue(v)"),
            $"Expected 'new IntValue(v)' but got: {result}"
        );
        Assert.IsTrue(
            result.Contains("new ListValue(intListArg.Select(v => new IntValue(v)"),
            $"Expected proper list value construction but got: {result}"
        );
    }

    [TestMethod]
    public void RenderArgumentValue_ScalarBool_GeneratesBooleanValue()
    {
        // Arrange
        var arg = new InputValue
        {
            Name = "enabled",
            Description = "Whether to enable",
            Type = CreateBooleanTypeRef(nullable: false),
        };

        // Act
        var result = CodeRenderer.RenderArgumentValue(arg);

        // Assert
        Assert.IsTrue(
            result.Contains("new BooleanValue(enabled)"),
            $"Expected 'new BooleanValue(enabled)' but got: {result}"
        );
    }
}
