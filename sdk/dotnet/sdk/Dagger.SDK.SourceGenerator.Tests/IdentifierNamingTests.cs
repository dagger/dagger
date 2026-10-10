using System.Collections.Generic;
using System.Collections.Immutable;
using System.Linq;
using Dagger.SDK.SourceGenerator.Code;
using Dagger.SDK.SourceGenerator.Tests.Utils;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.CSharp.Syntax;
using Microsoft.VisualStudio.TestTools.UnitTesting;

namespace Dagger.SDK.SourceGenerator.Tests;

[TestClass]
public class IdentifierNamingTests
{
    [TestMethod]
    public void CoreClientCompilesWithFormattedNames()
    {
        var code = Generate(Fixtures.CoreSchema, Fixtures.CoreNames());

        StringAssert.Contains(code, " WithMcpServer(");
        var errors = Fixtures.CompileWithRuntime(code);
        Assert.AreEqual(0, errors.Length, string.Join("\n", errors.Take(20)));
    }

    [TestMethod]
    public void CoreClientCompilesWithoutFormattedNames()
    {
        var code = Generate(Fixtures.CoreSchema);

        Assert.IsFalse(code.Contains(" WithMcpServer("));
        var errors = Fixtures.CompileWithRuntime(code);
        Assert.AreEqual(0, errors.Length, string.Join("\n", errors.Take(20)));
    }

    [TestMethod]
    public void NamesMembersFromFormattedNames()
    {
        var code = Generate(NamesSchema, Names());

        // Methods are PascalCase and parameters camelCase, acronyms written
        // like words.
        var method = Method(code, "Container", "WithMcpServer");
        CollectionAssert.AreEqual(
            new[] { "name", "insecureSkipTlsVerify" },
            method.ParameterList.Parameters.Select(p => p.Identifier.ValueText).ToArray()
        );

        // The schema's names go over the wire unchanged.
        StringAssert.Contains(code, "QueryBuilder.Select(\"withMCPServer\"");
        StringAssert.Contains(code, "new Argument(\"insecureSkipTLSVerify\"");
        StringAssert.Contains(code, "new KeyValuePair<string, Value>(\"commitSHA\"");

        // Input object properties are PascalCase.
        StringAssert.Contains(code, "public string CommitSha { get; } = commitSha;");

        // Types and enum values keep the schema's names.
        StringAssert.Contains(code, "public class Container(");
        StringAssert.Contains(code, "public enum NetworkProtocol");
        StringAssert.Contains(code, "TCP");
    }

    [TestMethod]
    public void KeepsLegacyNamesAsObsoleteForwarders()
    {
        var code = Generate(NamesSchema, Names());

        // withMCPServer was WithMcpserver, with the schema's parameter names.
        var legacy = Method(code, "Container", "WithMcpserver");
        StringAssert.Contains(
            legacy.AttributeLists.ToString(),
            "Obsolete(\"Use WithMcpServer instead.\")"
        );
        CollectionAssert.AreEqual(
            new[] { "name", "insecureSkipTLSVerify" },
            legacy.ParameterList.Parameters.Select(p => p.Identifier.ValueText).ToArray()
        );
        Assert.AreEqual(
            "WithMcpServer(name, insecureSkipTLSVerify)",
            legacy.ExpressionBody!.Expression.ToString()
        );

        // commitSHA was CommitSha already, so it gets no forwarder; the
        // property renamed by its formatted name gets one.
        StringAssert.Contains(code, "public bool SkipTlsVerify { get; } = skipTlsVerify;");
        StringAssert.Contains(code, "[Obsolete(\"Use SkipTlsVerify instead.\")]");
        StringAssert.Contains(code, "public bool SkipTlsverify => SkipTlsVerify;");
        Assert.IsFalse(code.Contains("Use CommitSha instead."));

        // A name its formatted name doesn't change gets no forwarder.
        Assert.AreEqual(1, Methods(code, "Container", "IdAsync").Length);

        // Interfaces keep legacy names as default implementations.
        var legacyInterfaceMethod = Method(code, "Runner", "RunE2etestAsync");
        Assert.AreEqual(
            "RunE2eTestAsync(cancellationToken)",
            legacyInterfaceMethod.ExpressionBody!.Expression.ToString()
        );
        Assert.AreEqual(1, Methods(code, "RunnerClient", "RunE2etestAsync").Length);
        Assert.AreEqual(1, Methods(code, "Container", "RunE2etestAsync").Length);

        var errors = Fixtures.CompileWithRuntime(code);
        Assert.AreEqual(0, errors.Length, string.Join("\n", errors.Take(20)));
    }

    [TestMethod]
    public void FallsBackForNamesMissingFromTheNamesFile()
    {
        var names = Names();
        foreach (var format in names.Values)
        {
            format.Remove("withMCPServer");
        }
        var code = Generate(NamesSchema, names);

        Assert.AreEqual(1, Methods(code, "Container", "WithMcpserver").Length);
        Assert.AreEqual(0, Methods(code, "Container", "WithMcpServer").Length);
        // Its arguments are still named from the file.
        var method = Method(code, "Container", "WithMcpserver");
        CollectionAssert.AreEqual(
            new[] { "name", "insecureSkipTlsVerify" },
            method.ParameterList.Parameters.Select(p => p.Identifier.ValueText).ToArray()
        );
    }

    [TestMethod]
    public void FallsBackForFormatsMissingFromTheNamesFile()
    {
        var names = Names();
        names.Remove(Namer.CamelFormat);
        var code = Generate(NamesSchema, names);

        // Methods follow PASCAL:CAPITALIZED; parameters keep the schema's
        // names.
        var method = Method(code, "Container", "WithMcpServer");
        CollectionAssert.AreEqual(
            new[] { "name", "insecureSkipTLSVerify" },
            method.ParameterList.Parameters.Select(p => p.Identifier.ValueText).ToArray()
        );
    }

    [TestMethod]
    public void SourceGeneratorReadsNamesFile()
    {
        var (withNames, _) = RunGenerator(
            new TestAdditionalFile("/sdk/Dagger.SDK/introspection.json", NamesSchema),
            new TestAdditionalFile("/sdk/Dagger.SDK/names.json", NamesFile)
        );
        StringAssert.Contains(withNames, "public Container WithMcpServer(");

        var (withoutNames, _) = RunGenerator(
            new TestAdditionalFile("/sdk/Dagger.SDK/introspection.json", NamesSchema)
        );
        Assert.IsFalse(withoutNames.Contains("WithMcpServer("));
        StringAssert.Contains(withoutNames, "public Container WithMcpserver(");
    }

    [TestMethod]
    public void SourceGeneratorRejectsInvalidNamesFile()
    {
        var (code, diagnostics) = RunGenerator(
            new TestAdditionalFile("/sdk/Dagger.SDK/introspection.json", NamesSchema),
            new TestAdditionalFile("/sdk/Dagger.SDK/names.json", "[]")
        );
        Assert.AreEqual("", code);
        Assert.IsTrue(diagnostics.Contains(SourceGenerator.FailedToParseNamesFile));
    }

    private static string Generate(
        string json,
        IReadOnlyDictionary<string, Dictionary<string, string>>? names = null
    ) => new CodeGenerator(new CodeRenderer()).Generate(Fixtures.Parse(json), names);

    private static (string, ImmutableArray<Diagnostic>) RunGenerator(params AdditionalText[] files)
    {
        var driver = CSharpGeneratorDriver
            .Create(new SourceGenerator())
            .AddAdditionalTexts([.. files])
            .RunGeneratorsAndUpdateCompilation(
                CSharpCompilation.Create(nameof(IdentifierNamingTests)),
                out _,
                out var diagnostics
            );
        var code = string.Concat(
            driver.GetRunResult().GeneratedTrees.Select(tree => tree.GetText().ToString())
        );
        return (code, diagnostics);
    }

    private static MethodDeclarationSyntax[] Methods(string code, string type, string name) =>
        CSharpSyntaxTree
            .ParseText(code)
            .GetRoot()
            .DescendantNodes()
            .OfType<TypeDeclarationSyntax>()
            .Where(t => t.Identifier.ValueText == type)
            .SelectMany(t => t.Members.OfType<MethodDeclarationSyntax>())
            .Where(m => m.Identifier.ValueText == name)
            .ToArray();

    private static MethodDeclarationSyntax Method(string code, string type, string name) =>
        Methods(code, type, name).Single();

    private static Dictionary<string, Dictionary<string, string>> Names() =>
        Namer.ParseNames(NamesFile);

    private const string NamesSchema = """
        {
          "__schema": {"types": [
            {"kind": "SCALAR", "name": "ID"},
            {"kind": "SCALAR", "name": "String"},
            {"kind": "SCALAR", "name": "Boolean"},
            {"kind": "ENUM", "name": "NetworkProtocol", "enumValues": [{"name": "TCP"}, {"name": "UDP"}]},
            {"kind": "INPUT_OBJECT", "name": "CommitInput", "inputFields": [
              {"name": "commitSHA", "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "String"}}},
              {"name": "skipTLSVerify", "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "Boolean"}}}
            ]},
            {"kind": "OBJECT", "name": "Query", "fields": [
              {"name": "container", "type": {"kind": "NON_NULL", "ofType": {"kind": "OBJECT", "name": "Container"}}}
            ]},
            {"kind": "INTERFACE", "name": "Runner", "fields": [
              {"name": "id", "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "ID"}}},
              {"name": "runE2ETest", "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "String"}}}
            ]},
            {"kind": "OBJECT", "name": "Container", "interfaces": [{"kind": "INTERFACE", "name": "Runner"}], "fields": [
              {"name": "id", "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "ID"}}},
              {"name": "runE2ETest", "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "String"}}},
              {"name": "withMCPServer", "type": {"kind": "NON_NULL", "ofType": {"kind": "OBJECT", "name": "Container"}},
               "args": [
                 {"name": "name", "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "String"}}},
                 {"name": "insecureSkipTLSVerify", "type": {"kind": "SCALAR", "name": "Boolean"}, "defaultValue": "false"}
               ]}
            ]}
          ]}
        }
        """;

    // The names file `codegen introspect --names-out` writes for NamesSchema.
    private const string NamesFile = """
        {
          "PASCAL:CAPITALIZED": {
            "Boolean": "Boolean",
            "CommitInput": "CommitInput",
            "Container": "Container",
            "ID": "Id",
            "NetworkProtocol": "NetworkProtocol",
            "Query": "Query",
            "Runner": "Runner",
            "String": "String",
            "TCP": "Tcp",
            "UDP": "Udp",
            "commitSHA": "CommitSha",
            "container": "Container",
            "id": "Id",
            "insecureSkipTLSVerify": "InsecureSkipTlsVerify",
            "name": "Name",
            "runE2ETest": "RunE2eTest",
            "skipTLSVerify": "SkipTlsVerify",
            "withMCPServer": "WithMcpServer"
          },
          "CAMEL:CAPITALIZED": {
            "Boolean": "boolean",
            "CommitInput": "commitInput",
            "Container": "container",
            "ID": "id",
            "NetworkProtocol": "networkProtocol",
            "Query": "query",
            "Runner": "runner",
            "String": "string",
            "TCP": "tcp",
            "UDP": "udp",
            "commitSHA": "commitSha",
            "container": "container",
            "id": "id",
            "insecureSkipTLSVerify": "insecureSkipTlsVerify",
            "name": "name",
            "runE2ETest": "runE2eTest",
            "skipTLSVerify": "skipTlsVerify",
            "withMCPServer": "withMcpServer"
          }
        }
        """;
}
