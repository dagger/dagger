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
    public void KeepsTheNamesOfRenamedCoreNames()
    {
        var code = Generate(Fixtures.CoreSchema, Fixtures.CoreNames());

        // GitCommit.shortSHA was shortSha, whose method was ShortShaAsync, as
        // shortSHA's is: no forwarder named after the new schema name.
        Assert.AreEqual(1, Methods(code, "GitCommit", "ShortShaAsync").Length);
        Assert.AreEqual(0, Methods(code, "GitCommit", "ShortSHAAsync").Length);
        StringAssert.Contains(code, "QueryBuilder.Select(\"shortSHA\")");

        // So with LLMContentBlockInput.callID's property.
        StringAssert.Contains(code, "public string CallId { get; } = callId;");
        StringAssert.Contains(code, "new KeyValuePair<string, Value>(\"callID\"");
        Assert.IsFalse(code.Contains(" CallID =>"));

        // FunctionCachePolicy's members are named like the old values, so
        // they need no alias, and send the new ones.
        var policy = EnumDecl(code, "FunctionCachePolicy");
        CollectionAssert.AreEqual(
            new[] { "Default", "PerSession", "Never" },
            policy.Members.Select(m => m.Identifier.ValueText).ToArray()
        );
        Assert.AreEqual(
            "[GraphQLValue(\"PER_SESSION\")]",
            policy.Members[1].AttributeLists.ToString()
        );
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

        // Types are PascalCase too, with the GraphQL type names on the wire.
        StringAssert.Contains(code, "public class Container(");
        StringAssert.Contains(code, "public class JsonValue(");
        Assert.IsFalse(code.Contains("class JSONValue"));
        StringAssert.Contains(code, "[JsonConverter(typeof(ScalarIdConverter<Json>))]");
        StringAssert.Contains(code, "public class Json : Scalar");
        StringAssert.Contains(code, "public async Task<Json> ContentsAsync(");
        StringAssert.Contains(code, "public async Task<JsonValue[]> ValuesAsync(");
        StringAssert.Contains(
            code,
            "new JsonValue(Object.NodeQueryBuilder(id.Value, \"JSONValue\")"
        );
    }

    [TestMethod]
    public void NamesEnumMembersFromFormattedNames()
    {
        var code = Generate(NamesSchema, Names());

        // Members are PascalCase, carrying the schema values they go over
        // the wire as; the old members stay as obsolete aliases.
        var protocol = EnumDecl(code, "NetworkProtocol");
        StringAssert.Contains(
            protocol.AttributeLists.ToString(),
            "JsonConverter(typeof(GraphQLEnumConverter<NetworkProtocol>))"
        );
        CollectionAssert.AreEqual(
            new[] { "Tcp", "Udp", "TCP", "UDP" },
            protocol.Members.Select(m => m.Identifier.ValueText).ToArray()
        );
        Assert.AreEqual("[GraphQLValue(\"TCP\")]", protocol.Members[0].AttributeLists.ToString());
        Assert.AreEqual(
            "[Obsolete(\"Use Tcp instead.\")]",
            protocol.Members[2].AttributeLists.ToString()
        );
        Assert.AreEqual("Tcp", protocol.Members[2].EqualsValue!.Value.ToString());

        // A value and its alias share a member, sent as the first value.
        var compression = EnumDecl(code, "ImageLayerCompression");
        CollectionAssert.AreEqual(
            new[] { "Gzip", "GZIP" },
            compression.Members.Select(m => m.Identifier.ValueText).ToArray()
        );
        Assert.AreEqual(
            "[GraphQLValue(\"Gzip\", \"GZIP\")]",
            compression.Members[0].AttributeLists.ToString()
        );

        // Arguments go as the members' schema values, and defaults name the
        // new members.
        StringAssert.Contains(code, "new StringValue(GraphQLEnum.ToValue(compression))");
        StringAssert.Contains(code, "NetworkProtocol? protocol = NetworkProtocol.Tcp");

        var errors = Fixtures.CompileWithRuntime(code);
        Assert.AreEqual(0, errors.Length, string.Join("\n", errors.Take(20)));
    }

    [TestMethod]
    public void KeepsSchemaTypesAndEnumValuesWithoutFormattedNames()
    {
        var code = Generate(NamesSchema);

        StringAssert.Contains(code, "public class JSONValue(");
        StringAssert.Contains(code, "public class JSON : Scalar");
        StringAssert.Contains(
            code,
            "JsonConverter(typeof(JsonStringEnumConverter<NetworkProtocol>))"
        );
        CollectionAssert.AreEqual(
            new[] { "TCP", "UDP" },
            EnumDecl(code, "NetworkProtocol").Members.Select(m => m.Identifier.ValueText).ToArray()
        );
        StringAssert.Contains(code, "new StringValue(compression.ToString())");
        StringAssert.Contains(code, "NetworkProtocol? protocol = NetworkProtocol.TCP");
        Assert.IsFalse(code.Contains("GraphQLValue"));

        var errors = Fixtures.CompileWithRuntime(code);
        Assert.AreEqual(0, errors.Length, string.Join("\n", errors.Take(20)));
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

    private static EnumDeclarationSyntax EnumDecl(string code, string name) =>
        CSharpSyntaxTree
            .ParseText(code)
            .GetRoot()
            .DescendantNodes()
            .OfType<EnumDeclarationSyntax>()
            .Single(e => e.Identifier.ValueText == name);

    private static Dictionary<string, Dictionary<string, string>> Names() =>
        Namer.ParseNames(NamesFile);

    private const string NamesSchema = """
        {
          "__schema": {"types": [
            {"kind": "SCALAR", "name": "ID"},
            {"kind": "SCALAR", "name": "String"},
            {"kind": "SCALAR", "name": "Boolean"},
            {"kind": "SCALAR", "name": "JSON"},
            {"kind": "ENUM", "name": "NetworkProtocol", "enumValues": [{"name": "TCP"}, {"name": "UDP"}]},
            {"kind": "ENUM", "name": "ImageLayerCompression", "enumValues": [{"name": "Gzip"}, {"name": "GZIP"}]},
            {"kind": "INPUT_OBJECT", "name": "CommitInput", "inputFields": [
              {"name": "commitSHA", "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "String"}}},
              {"name": "skipTLSVerify", "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "Boolean"}}}
            ]},
            {"kind": "OBJECT", "name": "Query", "fields": [
              {"name": "container", "type": {"kind": "NON_NULL", "ofType": {"kind": "OBJECT", "name": "Container"}}}
            ]},
            {"kind": "OBJECT", "name": "JSONValue", "fields": [
              {"name": "id", "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "ID"}}},
              {"name": "contents", "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "JSON"}}}
            ]},
            {"kind": "INTERFACE", "name": "Runner", "fields": [
              {"name": "id", "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "ID"}}},
              {"name": "runE2ETest", "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "String"}}}
            ]},
            {"kind": "OBJECT", "name": "Container", "interfaces": [{"kind": "INTERFACE", "name": "Runner"}], "fields": [
              {"name": "id", "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "ID"}}},
              {"name": "runE2ETest", "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "String"}}},
              {"name": "values", "type": {"kind": "NON_NULL", "ofType": {"kind": "LIST", "ofType": {"kind": "NON_NULL", "ofType": {"kind": "OBJECT", "name": "JSONValue"}}}}},
              {"name": "withCompression", "type": {"kind": "NON_NULL", "ofType": {"kind": "OBJECT", "name": "Container"}},
               "args": [
                 {"name": "compression", "type": {"kind": "NON_NULL", "ofType": {"kind": "ENUM", "name": "ImageLayerCompression"}}},
                 {"name": "protocol", "type": {"kind": "ENUM", "name": "NetworkProtocol"}, "defaultValue": "TCP"}
               ]},
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
            "GZIP": "Gzip",
            "Gzip": "Gzip",
            "ID": "Id",
            "ImageLayerCompression": "ImageLayerCompression",
            "JSON": "Json",
            "JSONValue": "JsonValue",
            "NetworkProtocol": "NetworkProtocol",
            "Query": "Query",
            "Runner": "Runner",
            "String": "String",
            "TCP": "Tcp",
            "UDP": "Udp",
            "commitSHA": "CommitSha",
            "compression": "Compression",
            "container": "Container",
            "contents": "Contents",
            "id": "Id",
            "insecureSkipTLSVerify": "InsecureSkipTlsVerify",
            "name": "Name",
            "protocol": "Protocol",
            "runE2ETest": "RunE2eTest",
            "skipTLSVerify": "SkipTlsVerify",
            "values": "Values",
            "withCompression": "WithCompression",
            "withMCPServer": "WithMcpServer"
          },
          "CAMEL:CAPITALIZED": {
            "Boolean": "boolean",
            "CommitInput": "commitInput",
            "Container": "container",
            "GZIP": "gzip",
            "Gzip": "gzip",
            "ID": "id",
            "ImageLayerCompression": "imageLayerCompression",
            "JSON": "json",
            "JSONValue": "jsonValue",
            "NetworkProtocol": "networkProtocol",
            "Query": "query",
            "Runner": "runner",
            "String": "string",
            "TCP": "tcp",
            "UDP": "udp",
            "commitSHA": "commitSha",
            "compression": "compression",
            "container": "container",
            "contents": "contents",
            "id": "id",
            "insecureSkipTLSVerify": "insecureSkipTlsVerify",
            "name": "name",
            "protocol": "protocol",
            "runE2ETest": "runE2eTest",
            "skipTLSVerify": "skipTlsVerify",
            "values": "values",
            "withCompression": "withCompression",
            "withMCPServer": "withMcpServer"
          }
        }
        """;
}
