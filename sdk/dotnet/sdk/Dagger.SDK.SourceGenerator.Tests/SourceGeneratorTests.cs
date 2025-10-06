using System.Collections.Immutable;
using System.IO;
using System.Linq;
using Dagger.SDK.SourceGenerator.Tests.Utils;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.VisualStudio.TestTools.UnitTesting;

namespace Dagger.SDK.SourceGenerator.Tests;

[TestClass]
public class SourceGeneratorTests
{
    [TestMethod]
    [DataRow("introspection.json", TestData.Schema)]
    public void GenerateCodeBasedOnSchema(string path, string text)
    {
        // Arrange
        var generator = new SourceGenerator();
        var driver = CSharpGeneratorDriver.Create(generator);
        var compilation = CSharpCompilation.Create(nameof(SourceGeneratorTests));

        // Act
        var result = driver
            .AddAdditionalTexts([new TestAdditionalFile(path, text)])
            .RunGeneratorsAndUpdateCompilation(
                compilation,
                out Compilation outputCompilation,
                out ImmutableArray<Diagnostic> diagnostics
            )
            .GetRunResult();

        var files = outputCompilation
            .SyntaxTrees.Select(t => Path.GetFileName(t.FilePath))
            .ToArray();

        // Assert
        CollectionAssert.Contains(
            collection: files,
            element: "Dagger.SDK.g.cs",
            message: "Generated file not found."
        );
    }

    [TestMethod]
    public void GenerateNoCodeWhenNoAdditionalFile()
    {
        // Arrange
        var generator = new SourceGenerator();
        var driver = CSharpGeneratorDriver.Create(generator);

        // Act
        var compilation = CSharpCompilation.Create(nameof(SourceGeneratorTests));
        var runResult = driver
            .AddAdditionalTexts(ImmutableArray<AdditionalText>.Empty)
            .RunGeneratorsAndUpdateCompilation(
                compilation,
                out Compilation outputCompilation,
                out ImmutableArray<Diagnostic> diagnostics
            )
            .GetRunResult();

        // Assert
        Assert.IsTrue(diagnostics.Contains(SourceGenerator.NoSchemaFileFound));
    }

    [TestMethod]
    [DataRow("introspection.json", "<xml></xml>")]
    public void GenerateNoCodeWhenInvalidJson(string path, string text)
    {
        // Arrange
        var generator = new SourceGenerator();
        var driver = CSharpGeneratorDriver.Create(generator);
        var compilation = CSharpCompilation.Create(nameof(SourceGeneratorTests));

        // Act
        var runResult = driver
            .AddAdditionalTexts([new TestAdditionalFile(path, text)])
            .RunGeneratorsAndUpdateCompilation(
                compilation,
                out Compilation outputCompilation,
                out ImmutableArray<Diagnostic> diagnostics
            )
            .GetRunResult();

        // Assert
        Assert.IsTrue(diagnostics.Contains(SourceGenerator.FailedToParseSchemaFile));
    }
}
