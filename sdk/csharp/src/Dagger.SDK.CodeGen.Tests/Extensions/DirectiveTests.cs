using System.Text.Json;
using Dagger.SDK.CodeGen.Extensions;
using Dagger.SDK.CodeGen.Types;

namespace Dagger.SDK.CodeGen.Tests.Extensions;

[TestClass]
public class DirectiveTests
{
    [TestMethod]
    public void HasDirective_WhenDirectiveExists_ReturnsTrue()
    {
        // Arrange
        var directives = new[]
        {
            new Directive { Name = "deprecated", Args = null },
            new Directive { Name = "experimental", Args = null },
        };

        // Act & Assert
        Assert.IsTrue(directives.HasDirective("deprecated"));
        Assert.IsTrue(directives.HasDirective("experimental"));
        Assert.IsFalse(directives.HasDirective("custom"));
    }

    [TestMethod]
    public void HasDirective_WhenDirectivesIsNull_ReturnsFalse()
    {
        // Arrange
        Directive[]? directives = null;

        // Act & Assert
        Assert.IsFalse(directives.HasDirective("deprecated"));
    }

    [TestMethod]
    public void GetDirective_WhenDirectiveExists_ReturnsDirective()
    {
        // Arrange
        var directives = new[]
        {
            new Directive { Name = "deprecated", Args = null },
            new Directive { Name = "experimental", Args = null },
        };

        // Act
        var deprecated = directives.GetDirective("deprecated");
        var experimental = directives.GetDirective("experimental");
        var missing = directives.GetDirective("custom");

        // Assert
        Assert.IsNotNull(deprecated);
        Assert.AreEqual("deprecated", deprecated.Name);
        Assert.IsNotNull(experimental);
        Assert.AreEqual("experimental", experimental.Name);
        Assert.IsNull(missing);
    }

    [TestMethod]
    public void GetDirectiveArgument_WithStringValue_ReturnsString()
    {
        // Arrange
        var directive = new Directive
        {
            Name = "deprecated",
            Args =
            [
                new DirectiveArg
                {
                    Name = "reason",
                    Value = JsonDocument.Parse("\"Use newMethod instead\"").RootElement,
                },
            ],
        };

        // Act
        var reason = directive.GetDirectiveArgument("reason");

        // Assert
        Assert.AreEqual("Use newMethod instead", reason);
    }

    [TestMethod]
    public void GetDirectiveArgument_WithNumberValue_ReturnsStringRepresentation()
    {
        // Arrange
        var directive = new Directive
        {
            Name = "custom",
            Args =
            [
                new DirectiveArg { Name = "version", Value = JsonDocument.Parse("42").RootElement },
            ],
        };

        // Act
        var version = directive.GetDirectiveArgument("version");

        // Assert
        Assert.AreEqual("42", version);
    }

    [TestMethod]
    public void GetDirectiveArgument_WithBooleanValue_ReturnsStringRepresentation()
    {
        // Arrange
        var directive = new Directive
        {
            Name = "custom",
            Args =
            [
                new DirectiveArg
                {
                    Name = "enabled",
                    Value = JsonDocument.Parse("true").RootElement,
                },
            ],
        };

        // Act
        var enabled = directive.GetDirectiveArgument("enabled");

        // Assert
        Assert.AreEqual("true", enabled);
    }

    [TestMethod]
    public void GetDirectiveArgument_WhenArgumentMissing_ReturnsNull()
    {
        // Arrange
        var directive = new Directive
        {
            Name = "deprecated",
            Args =
            [
                new DirectiveArg
                {
                    Name = "reason",
                    Value = JsonDocument.Parse("\"Some reason\"").RootElement,
                },
            ],
        };

        // Act
        var missing = directive.GetDirectiveArgument("nonexistent");

        // Assert
        Assert.IsNull(missing);
    }

    [TestMethod]
    public void IsExperimental_WhenExperimentalDirectiveExists_ReturnsTrue()
    {
        // Arrange
        var directives = new[]
        {
            new Directive { Name = "experimental", Args = null },
        };

        // Act & Assert
        Assert.IsTrue(directives.IsExperimental());
    }

    [TestMethod]
    public void IsExperimental_WhenNoExperimentalDirective_ReturnsFalse()
    {
        // Arrange
        var directives = new[]
        {
            new Directive { Name = "deprecated", Args = null },
        };

        // Act & Assert
        Assert.IsFalse(directives.IsExperimental());
    }

    [TestMethod]
    public void GetExperimentalReason_WithReason_ReturnsReason()
    {
        // Arrange
        var directives = new[]
        {
            new Directive
            {
                Name = "experimental",
                Args =
                [
                    new DirectiveArg
                    {
                        Name = "reason",
                        Value = JsonDocument.Parse("\"This API is experimental\"").RootElement,
                    },
                ],
            },
        };

        // Act
        var reason = directives.GetExperimentalReason();

        // Assert
        Assert.AreEqual("This API is experimental", reason);
    }

    [TestMethod]
    public void GetExperimentalReason_WithoutReason_ReturnsNull()
    {
        // Arrange
        var directives = new[]
        {
            new Directive { Name = "experimental", Args = null },
        };

        // Act
        var reason = directives.GetExperimentalReason();

        // Assert
        Assert.IsNull(reason);
    }

    [TestMethod]
    public void HasDirective_Deprecated_WhenDeprecatedDirectiveExists_ReturnsTrue()
    {
        // Arrange
        var directives = new[]
        {
            new Directive { Name = "deprecated", Args = null },
        };

        // Act & Assert
        Assert.IsTrue(directives.HasDirective("deprecated"));
    }

    [TestMethod]
    public void GetDirective_Deprecated_WithReason_ReturnsReason()
    {
        // Arrange
        var directives = new[]
        {
            new Directive
            {
                Name = "deprecated",
                Args =
                [
                    new DirectiveArg
                    {
                        Name = "reason",
                        Value = JsonDocument.Parse("\"Use newMethod instead\"").RootElement,
                    },
                ],
            },
        };

        // Act
        var deprecated = directives.GetDirective("deprecated");
        var reason = deprecated.GetDirectiveArgument("reason");

        // Assert
        Assert.AreEqual("Use newMethod instead", reason);
    }

    [TestMethod]
    public void DirectiveDeserialization_FromJson_WorksCorrectly()
    {
        // Arrange
        var json = """
            [
                {
                    "name": "deprecated",
                    "args": [
                        {
                            "name": "reason",
                            "value": "Use alternative method"
                        }
                    ]
                },
                {
                    "name": "experimental",
                    "args": []
                }
            ]
            """;

        // Act
        var directives = JsonSerializer.Deserialize<Directive[]>(json);

        // Assert
        Assert.IsNotNull(directives);
        Assert.AreEqual(2, directives.Length);
        Assert.AreEqual("deprecated", directives[0].Name);
        Assert.AreEqual("experimental", directives[1].Name);
        Assert.IsNotNull(directives[0].Args);
        Assert.AreEqual(1, directives[0].Args!.Length);
        Assert.AreEqual("reason", directives[0].Args![0].Name);
    }
}
