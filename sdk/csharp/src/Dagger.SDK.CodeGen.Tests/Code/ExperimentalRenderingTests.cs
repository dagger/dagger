using Dagger.SDK.CodeGen.Code;
using Dagger.SDK.CodeGen.Types;

namespace Dagger.SDK.CodeGen.Tests.Code;

[TestClass]
public class ExperimentalRenderingTests
{
    private CodeRenderer _renderer = null!;

    [TestInitialize]
    public void Setup()
    {
        _renderer = new CodeRenderer();
    }

    private static TypeRef CreateIdTypeRef()
    {
        return new TypeRef
        {
            Kind = TypeKind.NON_NULL,
            OfType = new TypeRef { Kind = TypeKind.SCALAR, Name = "ID" },
        };
    }

    private static TypeRef CreateStringTypeRef()
    {
        return new TypeRef { Kind = TypeKind.SCALAR, Name = "String" };
    }

    private static TypeRef CreateNonNullStringTypeRef()
    {
        return new TypeRef
        {
            Kind = TypeKind.NON_NULL,
            OfType = new TypeRef { Kind = TypeKind.SCALAR, Name = "String" },
        };
    }

    [TestMethod]
    public void RenderObject_WithExperimentalDirective_IncludesExperimentalAttribute()
    {
        // Arrange
        var type = new Types.Type
        {
            Name = "ExperimentalContainer",
            Description = "An experimental container type",
            Kind = TypeKind.OBJECT,
            Fields =
            [
                new Field
                {
                    Name = "id",
                    Description = "The unique identifier",
                    Type = CreateIdTypeRef(),
                    Args = Array.Empty<InputValue>(),
                    DeprecationReason = "",
                    Directives = null,
                },
            ],
            Directives = [new Directive { Name = "experimental", Args = null }],
        };

        // Wire up parent reference
        type.Fields[0].ParentType = type;

        // Act
        var result = _renderer.RenderObject(type);

        // Assert - Just verify experimental attribute is present
        Assert.IsTrue(result.Contains("[System.Diagnostics.CodeAnalysis.Experimental(\"DAGGER_"));
        Assert.IsTrue(result.Contains("public class ExperimentalContainer"));
    }

    [TestMethod]
    public void RenderObject_WithoutExperimentalDirective_DoesNotIncludeExperimentalAttribute()
    {
        // Arrange
        var type = new Types.Type
        {
            Name = "RegularContainer",
            Description = "A regular container type",
            Kind = TypeKind.OBJECT,
            Fields =
            [
                new Field
                {
                    Name = "id",
                    Description = "The unique identifier",
                    Type = CreateIdTypeRef(),
                    Args = Array.Empty<InputValue>(),
                    DeprecationReason = "",
                    Directives = null,
                },
            ],
            Directives = null,
        };

        // Wire up parent reference
        type.Fields[0].ParentType = type;

        // Act
        var result = _renderer.RenderObject(type);

        // Assert
        Assert.IsFalse(result.Contains("[System.Diagnostics.CodeAnalysis.Experimental"));
        Assert.IsTrue(result.Contains("public class RegularContainer"));
    }

    [TestMethod]
    public void RenderField_WithExperimentalDirective_IncludesExperimentalAttribute()
    {
        // Arrange
        var type = new Types.Type
        {
            Name = "Container",
            Description = "A container type",
            Kind = TypeKind.OBJECT,
            Fields =
            [
                new Field
                {
                    Name = "id",
                    Description = "The unique identifier",
                    Type = CreateIdTypeRef(),
                    Args = Array.Empty<InputValue>(),
                    DeprecationReason = "",
                    Directives = null,
                },
                new Field
                {
                    Name = "experimentalMethod",
                    Description = "An experimental method",
                    Type = CreateStringTypeRef(),
                    Args = Array.Empty<InputValue>(),
                    DeprecationReason = "",
                    Directives = [new Directive { Name = "experimental", Args = null }],
                },
            ],
            Directives = null,
        };

        // Wire up parent references
        foreach (var field in type.Fields)
        {
            field.ParentType = type;
        }

        // Act
        var result = _renderer.RenderObject(type);

        // Assert - Method-level experimental should have unique diagnostic ID
        Assert.IsTrue(result.Contains("[System.Diagnostics.CodeAnalysis.Experimental(\"DAGGER_"));
    }

    [TestMethod]
    public void RenderEnum_WithExperimentalDirective_IncludesExperimentalAttribute()
    {
        // Arrange
        var type = new Types.Type
        {
            Name = "ExperimentalStatus",
            Description = "An experimental status enum",
            Kind = TypeKind.ENUM,
            EnumValues =
            [
                new EnumValue
                {
                    Name = "SUCCESS",
                    Description = "Success status",
                    IsDeprecated = false,
                    Directives = null,
                },
                new EnumValue
                {
                    Name = "FAILURE",
                    Description = "Failure status",
                    IsDeprecated = false,
                    Directives = null,
                },
            ],
            Directives = [new Directive { Name = "experimental", Args = null }],
        };

        // Act
        var result = _renderer.RenderEnum(type);

        // Assert - Enum-level experimental should have unique diagnostic ID
        Assert.IsTrue(result.Contains("[System.Diagnostics.CodeAnalysis.Experimental(\"DAGGER_"));
        Assert.IsTrue(result.Contains("public enum ExperimentalStatus"));
    }

    [TestMethod]
    public void RenderInputObject_WithExperimentalDirective_IncludesExperimentalAttribute()
    {
        // Arrange
        var type = new Types.Type
        {
            Name = "ExperimentalInput",
            Description = "An experimental input type",
            Kind = TypeKind.INPUT_OBJECT,
            InputFields =
            [
                new InputValue
                {
                    Name = "name",
                    Description = "The name",
                    Type = CreateNonNullStringTypeRef(),
                    DefaultValue = null,
                    Directives = null,
                },
            ],
            Directives = [new Directive { Name = "experimental", Args = null }],
        };

        // Act
        var result = _renderer.RenderInputObject(type);

        // Assert - Expect unique ID based on type name
        Assert.IsTrue(
            result.Contains(
                "[System.Diagnostics.CodeAnalysis.Experimental(\"DAGGER_EXPERIMENTALINPUT\""
            )
        );
        Assert.IsTrue(result.Contains("public struct ExperimentalInput"));
    }

    [TestMethod]
    public void RenderScalar_WithExperimentalDirective_IncludesExperimentalAttribute()
    {
        // Arrange
        var type = new Types.Type
        {
            Name = "ExperimentalScalar",
            Description = "An experimental scalar type",
            Kind = TypeKind.SCALAR,
            Directives = [new Directive { Name = "experimental", Args = null }],
        };

        // Act
        var result = _renderer.RenderScalar(type);

        // Assert - Expect unique ID based on type name
        Assert.IsTrue(
            result.Contains(
                "[System.Diagnostics.CodeAnalysis.Experimental(\"DAGGER_EXPERIMENTALSCALAR\""
            )
        );
        Assert.IsTrue(result.Contains("public class ExperimentalScalar"));
    }

    [TestMethod]
    public void ParentType_IsSetCorrectly_OnFields()
    {
        // Arrange
        var type = new Types.Type
        {
            Name = "Container",
            Description = "A container",
            Kind = TypeKind.OBJECT,
            Fields =
            [
                new Field
                {
                    Name = "id",
                    Description = "ID field",
                    Type = CreateIdTypeRef(),
                    Args = Array.Empty<InputValue>(),
                    DeprecationReason = "",
                    Directives = null,
                },
            ],
            Directives = null,
        };

        // Act
        type.Fields[0].ParentType = type;

        // Assert
        Assert.IsNotNull(type.Fields[0].ParentType);
        Assert.AreEqual("Container", type.Fields[0].ParentType!.Name);
        Assert.IsTrue(type.Fields[0].ProvidesId());
    }

    [TestMethod]
    public void ProvidesId_ReturnsTrueForIdField()
    {
        // Arrange
        var type = new Types.Type
        {
            Name = "Container",
            Description = "A container",
            Kind = TypeKind.OBJECT,
            Fields =
            [
                new Field
                {
                    Name = "id",
                    Description = "ID field",
                    Type = CreateIdTypeRef(),
                    Args = Array.Empty<InputValue>(),
                    DeprecationReason = "",
                    Directives = null,
                },
            ],
            Directives = null,
        };

        type.Fields[0].ParentType = type;

        // Act
        var providesId = type.Fields[0].ProvidesId();

        // Assert
        Assert.IsTrue(providesId);
    }

    [TestMethod]
    public void ProvidesId_ReturnsFalseForNonIdField()
    {
        // Arrange
        var type = new Types.Type
        {
            Name = "Container",
            Description = "A container",
            Kind = TypeKind.OBJECT,
            Fields =
            [
                new Field
                {
                    Name = "name",
                    Description = "Name field",
                    Type = CreateNonNullStringTypeRef(),
                    Args = Array.Empty<InputValue>(),
                    DeprecationReason = "",
                    Directives = null,
                },
            ],
            Directives = null,
        };

        type.Fields[0].ParentType = type;

        // Act
        var providesId = type.Fields[0].ProvidesId();

        // Assert
        Assert.IsFalse(providesId);
    }

    [TestMethod]
    public void GetIdField_ReturnsCorrectField()
    {
        // Arrange
        var type = new Types.Type
        {
            Name = "Container",
            Description = "A container",
            Kind = TypeKind.OBJECT,
            Fields =
            [
                new Field
                {
                    Name = "id",
                    Description = "ID field",
                    Type = CreateIdTypeRef(),
                    Args = Array.Empty<InputValue>(),
                    DeprecationReason = "",
                    Directives = null,
                },
                new Field
                {
                    Name = "name",
                    Description = "Name field",
                    Type = CreateStringTypeRef(),
                    Args = Array.Empty<InputValue>(),
                    DeprecationReason = "",
                    Directives = null,
                },
            ],
            Directives = null,
        };

        foreach (var field in type.Fields)
        {
            field.ParentType = type;
        }

        // Act
        var idField = type.Fields[1].GetIdField();

        // Assert
        Assert.IsNotNull(idField);
        Assert.AreEqual("id", idField.Name);
    }
}
