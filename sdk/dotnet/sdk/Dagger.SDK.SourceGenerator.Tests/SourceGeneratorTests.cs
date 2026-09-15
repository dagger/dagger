using System.Collections.Immutable;
using System.IO;
using System.Linq;
using System.Text.Json;
using Dagger.SDK.SourceGenerator.Code;
using Dagger.SDK.SourceGenerator.Tests.Utils;
using Dagger.SDK.SourceGenerator.Types;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.VisualStudio.TestTools.UnitTesting;

namespace Dagger.SDK.SourceGenerator.Tests;

[TestClass]
public class SourceGeneratorTests
{
    [TestMethod]
    [DataRow("v0.21.0-dev")]
    [DataRow("v1.0.0-beta.9")]
    [DataRow("v1.0.0-beta.9-dev")]
    public void NullableObjectsKeepOlderEngineShape(string version)
    {
        var introspection = NullableObjectIntrospection(version);
        var code = new CodeGenerator(new CodeRenderer()).Generate(introspection);

        StringAssert.Contains(code, "public GitRef LatestVersion()");
        Assert.IsFalse(code.Contains("LatestVersionAsync"));
    }

    [TestMethod]
    [DataRow(null)]
    [DataRow("")]
    [DataRow("development")]
    [DataRow("v1.0.0-beta.10")]
    [DataRow("v1.0.0-beta.10-dev")]
    [DataRow("v1.0.0-rc.1")]
    [DataRow("v1.0.0")]
    public void NullableObjectsUseResolvedShapeFromBeta10(string? version)
    {
        var introspection = NullableObjectIntrospection(version);
        var code = new CodeGenerator(new CodeRenderer()).Generate(introspection);

        StringAssert.Contains(code, "public async Task<GitRef?> LatestVersionAsync");
    }

    [TestMethod]
    public void PreservesAcronymsInEnumTypeNames()
    {
        var introspection = new Introspection
        {
            Schema = new Schema
            {
                Types =
                [
                    new Dagger.SDK.SourceGenerator.Types.Type
                    {
                        Kind = "ENUM",
                        Name = "LLMMessageRole",
                        EnumValues = [new EnumValue { Name = "USER" }],
                    },
                ],
            },
        };

        var code = new CodeGenerator(new CodeRenderer()).Generate(introspection);

        StringAssert.Contains(code, "JsonStringEnumConverter<LLMMessageRole>");
        StringAssert.Contains(code, "public enum LLMMessageRole");
    }

    [TestMethod]
    public void FormatsIdScalarName()
    {
        Assert.AreEqual("Id", Formatter.FormatType("ID"));
    }

    [TestMethod]
    public void ImplementsInterfacesWithCovariantReturnsAndAdditionalOptionalArguments()
    {
        var introspection = InterfaceImplementationIntrospection();

        var code = new CodeGenerator(new CodeRenderer()).Generate(introspection);

        StringAssert.Contains(code, "async Task<Syncer> Syncer.SyncAsync");
        StringAssert.Contains(code, "return await SyncAsync(cancellationToken: cancellationToken)");
        StringAssert.Contains(code, "async Task<string> Exportable.ExportAsync");
        StringAssert.Contains(
            code,
            "return await ExportAsync(path: path, cancellationToken: cancellationToken)"
        );
    }

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

    private static Introspection NullableObjectIntrospection(string? version) =>
        new()
        {
            SchemaVersion = version,
            Schema = new Schema
            {
                Types =
                [
                    new Dagger.SDK.SourceGenerator.Types.Type
                    {
                        Kind = "OBJECT",
                        Name = "GitRepository",
                        Fields =
                        [
                            new Field
                            {
                                Name = "latestVersion",
                                Type = new TypeRef { Kind = "OBJECT", Name = "GitRef" },
                            },
                        ],
                    },
                    new Dagger.SDK.SourceGenerator.Types.Type { Kind = "OBJECT", Name = "GitRef" },
                ],
            },
        };

    private static Introspection InterfaceImplementationIntrospection() =>
        JsonSerializer.Deserialize<Introspection>(InterfaceImplementationSchema)!;

    private const string InterfaceImplementationSchema = """
        {"__schema":{"types":[
          {"kind":"INTERFACE","name":"Syncer","fields":[
            {"name":"sync","type":{"kind":"NON_NULL","ofType":{"kind":"SCALAR","name":"ID"}},
             "directives":[{"name":"expectedType","args":[{"name":"name","value":"\"Syncer\""}]}]}
          ]},
          {"kind":"INTERFACE","name":"Exportable","fields":[
            {"name":"export","type":{"kind":"NON_NULL","ofType":{"kind":"SCALAR","name":"String"}},
             "args":[{"name":"path","type":{"kind":"NON_NULL","ofType":{"kind":"SCALAR","name":"String"}}}]}
          ]},
          {"kind":"OBJECT","name":"Container",
           "interfaces":[{"kind":"INTERFACE","name":"Syncer"},{"kind":"INTERFACE","name":"Exportable"}],
           "fields":[
             {"name":"sync","type":{"kind":"NON_NULL","ofType":{"kind":"SCALAR","name":"ID"}},
              "directives":[{"name":"expectedType","args":[{"name":"name","value":"\"Container\""}]}]},
             {"name":"export","type":{"kind":"NON_NULL","ofType":{"kind":"SCALAR","name":"String"}},
              "args":[
                {"name":"path","type":{"kind":"NON_NULL","ofType":{"kind":"SCALAR","name":"String"}}},
                {"name":"expand","type":{"kind":"SCALAR","name":"Boolean"}}
              ]}
           ]}
        ]}}
        """;
}
