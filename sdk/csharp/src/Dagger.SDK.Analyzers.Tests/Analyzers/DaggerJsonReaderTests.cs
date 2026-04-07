using System.Collections.Immutable;
using Dagger.SDK.Analyzers.Tests.Helpers;
using Microsoft.CodeAnalysis;

namespace Dagger.SDK.Analyzers.Tests.Analyzers;

/// <summary>
/// Tests for DaggerJsonReader utility class.
/// </summary>
[TestClass]
public class DaggerJsonReaderTests
{
    #region FindDaggerJson Tests

    [TestMethod]
    public void FindDaggerJson_WithDaggerJsonFile_ReturnsConfig()
    {
        var additionalFiles = ImmutableArray.Create<AdditionalText>(
            new TestAdditionalFile(
                "dagger.json",
                """
                {
                  "name": "my-module",
                  "source": "."
                }
                """
            )
        );

        var result = DaggerJsonReader.FindDaggerJson(additionalFiles);

        Assert.IsNotNull(result);
        Assert.AreEqual("my-module", result.Name);
        Assert.AreEqual(".", result.Source);
    }

    [TestMethod]
    public void FindDaggerJson_WithoutDaggerJsonFile_ReturnsNull()
    {
        var additionalFiles = ImmutableArray.Create<AdditionalText>(
            new TestAdditionalFile("some-other-file.json", "{}")
        );

        var result = DaggerJsonReader.FindDaggerJson(additionalFiles);

        Assert.IsNull(result);
    }

    [TestMethod]
    public void FindDaggerJson_WithEmptyArray_ReturnsNull()
    {
        var additionalFiles = ImmutableArray<AdditionalText>.Empty;

        var result = DaggerJsonReader.FindDaggerJson(additionalFiles);

        Assert.IsNull(result);
    }

    [TestMethod]
    public void FindDaggerJson_WithMultipleDaggerJsonFiles_ReturnsFirst()
    {
        var additionalFiles = ImmutableArray.Create<AdditionalText>(
            new TestAdditionalFile("dagger.json", """{"name": "first", "source": "."}"""),
            new TestAdditionalFile("other/dagger.json", """{"name": "second", "source": "."}""")
        );

        var result = DaggerJsonReader.FindDaggerJson(additionalFiles);

        Assert.IsNotNull(result);
        Assert.AreEqual("first", result.Name);
    }

    [TestMethod]
    public void FindDaggerJson_CaseInsensitive_ReturnsConfig()
    {
        var additionalFiles = ImmutableArray.Create<AdditionalText>(
            new TestAdditionalFile("DAGGER.JSON", """{"name": "my-module", "source": "."}""")
        );

        var result = DaggerJsonReader.FindDaggerJson(additionalFiles);

        Assert.IsNotNull(result);
        Assert.AreEqual("my-module", result.Name);
    }

    [TestMethod]
    public void FindDaggerJson_MissingNameField_ReturnsNull()
    {
        var additionalFiles = ImmutableArray.Create<AdditionalText>(
            new TestAdditionalFile("dagger.json", """{"source": "."}""")
        );

        var result = DaggerJsonReader.FindDaggerJson(additionalFiles);

        Assert.IsNull(result);
    }

    [TestMethod]
    public void FindDaggerJson_MalformedJson_ReturnsNull()
    {
        var additionalFiles = ImmutableArray.Create<AdditionalText>(
            new TestAdditionalFile("dagger.json", "{ invalid json }")
        );

        var result = DaggerJsonReader.FindDaggerJson(additionalFiles);

        Assert.IsNull(result);
    }

    [TestMethod]
    public void FindDaggerJson_DefaultSourceWhenMissing()
    {
        var additionalFiles = ImmutableArray.Create<AdditionalText>(
            new TestAdditionalFile("dagger.json", """{"name": "my-module"}""")
        );

        var result = DaggerJsonReader.FindDaggerJson(additionalFiles);

        Assert.IsNotNull(result);
        Assert.AreEqual(".", result.Source); // Default value
    }

    #endregion

    #region FormatName Tests

    [TestMethod]
    [DataRow("my-module", "MyModule")]
    [DataRow("my_module", "MyModule")]
    [DataRow("my.module", "MyModule")]
    [DataRow("my module", "MyModule")]
    [DataRow("MyModule", "MyModule")]
    [DataRow("myModule", "MyModule")]
    [DataRow("MYMODULE", "Mymodule")]
    [DataRow("friendly-bard", "FriendlyBard")]
    [DataRow("friendly_bard", "FriendlyBard")]
    [DataRow("Friendly-Bard", "FriendlyBard")]
    [DataRow("FRIENDLY-BARD", "FriendlyBard")]
    [DataRow("friendly--bard", "FriendlyBard")]
    [DataRow("friendly-.bard", "FriendlyBard")]
    [DataRow("_friendly_bard_", "FriendlyBard")]
    [DataRow("--friendly-bard--", "FriendlyBard")]
    [DataRow(" friendly bard ", "FriendlyBard")]
    [DataRow("2module", "Module")] // Leading digits removed
    [DataRow("9test-module", "TestModule")] // Leading digits removed
    [DataRow("", "DaggerModule")] // Empty string fallback
    [DataRow("   ", "DaggerModule")] // Whitespace fallback
    [DataRow("123", "DaggerModule")] // All digits fallback
    [DataRow("---", "DaggerModule")] // All separators fallback
    public void FormatName_TransformsCorrectly(string input, string expected)
    {
        var result = DaggerJsonReader.FormatName(input);

        Assert.AreEqual(expected, result);
    }

    #endregion

    #region ParseDaggerJson Tests - Testing via FindDaggerJson since ParseDaggerJson is private

    [TestMethod]
    public void ParseDaggerJson_ValidJson_ReturnsNameAndSource()
    {
        var additionalFiles = ImmutableArray.Create<AdditionalText>(
            new TestAdditionalFile(
                "dagger.json",
                """
                {
                  "name": "my-module",
                  "source": "src"
                }
                """
            )
        );

        var config = DaggerJsonReader.FindDaggerJson(additionalFiles);

        Assert.IsNotNull(config);
        Assert.AreEqual("my-module", config.Name);
        Assert.AreEqual("src", config.Source);
    }

    [TestMethod]
    public void ParseDaggerJson_MissingNameField_ReturnsNull()
    {
        var additionalFiles = ImmutableArray.Create<AdditionalText>(
            new TestAdditionalFile(
                "dagger.json",
                """
                {
                  "source": "."
                }
                """
            )
        );

        var config = DaggerJsonReader.FindDaggerJson(additionalFiles);

        Assert.IsNull(config);
    }

    [TestMethod]
    public void ParseDaggerJson_MissingSourceField_UsesDefault()
    {
        var additionalFiles = ImmutableArray.Create<AdditionalText>(
            new TestAdditionalFile(
                "dagger.json",
                """
                {
                  "name": "my-module"
                }
                """
            )
        );

        var config = DaggerJsonReader.FindDaggerJson(additionalFiles);

        Assert.IsNotNull(config);
        Assert.AreEqual("my-module", config.Name);
        Assert.AreEqual(".", config.Source);
    }

    [TestMethod]
    public void ParseDaggerJson_EmptyJson_ReturnsNull()
    {
        var additionalFiles = ImmutableArray.Create<AdditionalText>(
            new TestAdditionalFile("dagger.json", "{}")
        );

        var config = DaggerJsonReader.FindDaggerJson(additionalFiles);

        Assert.IsNull(config);
    }

    [TestMethod]
    public void ParseDaggerJson_MalformedJson_ReturnsNull()
    {
        var additionalFiles = ImmutableArray.Create<AdditionalText>(
            new TestAdditionalFile("dagger.json", "{ invalid json }")
        );

        var config = DaggerJsonReader.FindDaggerJson(additionalFiles);

        Assert.IsNull(config);
    }

    [TestMethod]
    public void ParseDaggerJson_WithExtraFields_IgnoresExtra()
    {
        var additionalFiles = ImmutableArray.Create<AdditionalText>(
            new TestAdditionalFile(
                "dagger.json",
                """
                {
                  "name": "my-module",
                  "source": ".",
                  "sdk": "csharp",
                  "version": "1.0.0"
                }
                """
            )
        );

        var config = DaggerJsonReader.FindDaggerJson(additionalFiles);

        Assert.IsNotNull(config);
        Assert.AreEqual("my-module", config.Name);
        Assert.AreEqual(".", config.Source);
    }

    [TestMethod]
    public void ParseDaggerJson_WithNestedObjects_ParsesTopLevel()
    {
        var additionalFiles = ImmutableArray.Create<AdditionalText>(
            new TestAdditionalFile(
                "dagger.json",
                """
                {
                  "name": "my-module",
                  "source": ".",
                  "config": {
                    "nested": "value"
                  }
                }
                """
            )
        );

        var config = DaggerJsonReader.FindDaggerJson(additionalFiles);

        Assert.IsNotNull(config);
        Assert.AreEqual("my-module", config.Name);
        Assert.AreEqual(".", config.Source);
    }

    #endregion
}
