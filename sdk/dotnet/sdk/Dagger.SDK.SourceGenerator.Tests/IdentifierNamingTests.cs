using System.Linq;
using Dagger.SDK.SourceGenerator.Code;
using Dagger.SDK.SourceGenerator.Tests.Utils;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.CSharp.Syntax;
using Microsoft.VisualStudio.TestTools.UnitTesting;

namespace Dagger.SDK.SourceGenerator.Tests;

[TestClass]
public class IdentifierNamingTests
{
    [TestMethod]
    public void CoreClientCompilesWithIdentifierWords()
    {
        var code = Generate(Fixtures.CoreSchema);

        var errors = Fixtures.CompileWithRuntime(code);
        Assert.AreEqual(0, errors.Length, string.Join("\n", errors.Take(20)));
    }

    [TestMethod]
    public void CoreClientCompilesWithoutIdentifierWords()
    {
        var code = Generate(Fixtures.CoreSchemaWithoutIdentifiers());

        var errors = Fixtures.CompileWithRuntime(code);
        Assert.AreEqual(0, errors.Length, string.Join("\n", errors.Take(20)));
    }

    [TestMethod]
    public void NamesMembersFromIdentifierWords()
    {
        var code = Generate(WordsSchema);

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
        var code = Generate(WordsSchema);

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
        // property renamed by words gets one.
        StringAssert.Contains(code, "public bool SkipTlsVerify { get; } = skipTlsVerify;");
        StringAssert.Contains(code, "[Obsolete(\"Use SkipTlsVerify instead.\")]");
        StringAssert.Contains(code, "public bool SkipTlsverify => SkipTlsVerify;");
        Assert.IsFalse(code.Contains("Use CommitSha instead."));

        // A name unchanged by words gets no forwarder.
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
    public void FallsBackForNamesWithoutWords()
    {
        var code = Generate(WordsSchema.Replace("\"withMCPServer\":", "\"unused\":"));

        Assert.AreEqual(1, Methods(code, "Container", "WithMcpserver").Length);
        Assert.AreEqual(0, Methods(code, "Container", "WithMcpServer").Length);
    }

    private static string Generate(string json) =>
        new CodeGenerator(new CodeRenderer()).Generate(Fixtures.Parse(json));

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

    private const string WordsSchema = """
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
          ]},
          "__identifiers": {
            "Query": [{"kind": "WORD", "text": "query", "capitalized": "Query"}],
            "container": [{"kind": "WORD", "text": "container", "capitalized": "Container"}],
            "Runner": [{"kind": "WORD", "text": "runner", "capitalized": "Runner"}],
            "runE2ETest": [
              {"kind": "WORD", "text": "run", "capitalized": "Run"},
              {"kind": "ACRONYM", "text": "E2E", "capitalized": "E2e"},
              {"kind": "WORD", "text": "test", "capitalized": "Test"}
            ],
            "Container": [{"kind": "WORD", "text": "container", "capitalized": "Container"}],
            "NetworkProtocol": [
              {"kind": "WORD", "text": "network", "capitalized": "Network"},
              {"kind": "WORD", "text": "protocol", "capitalized": "Protocol"}
            ],
            "TCP": [{"kind": "ACRONYM", "text": "TCP", "capitalized": "Tcp"}],
            "UDP": [{"kind": "ACRONYM", "text": "UDP", "capitalized": "Udp"}],
            "CommitInput": [
              {"kind": "WORD", "text": "commit", "capitalized": "Commit"},
              {"kind": "WORD", "text": "input", "capitalized": "Input"}
            ],
            "commitSHA": [
              {"kind": "WORD", "text": "commit", "capitalized": "Commit"},
              {"kind": "ACRONYM", "text": "SHA", "capitalized": "Sha"}
            ],
            "skipTLSVerify": [
              {"kind": "WORD", "text": "skip", "capitalized": "Skip"},
              {"kind": "ACRONYM", "text": "TLS", "capitalized": "Tls"},
              {"kind": "WORD", "text": "verify", "capitalized": "Verify"}
            ],
            "id": [{"kind": "ACRONYM", "text": "ID", "capitalized": "Id"}],
            "withMCPServer": [
              {"kind": "WORD", "text": "with", "capitalized": "With"},
              {"kind": "ACRONYM", "text": "MCP", "capitalized": "Mcp"},
              {"kind": "WORD", "text": "server", "capitalized": "Server"}
            ],
            "name": [{"kind": "WORD", "text": "name", "capitalized": "Name"}],
            "insecureSkipTLSVerify": [
              {"kind": "WORD", "text": "insecure", "capitalized": "Insecure"},
              {"kind": "WORD", "text": "skip", "capitalized": "Skip"},
              {"kind": "ACRONYM", "text": "TLS", "capitalized": "Tls"},
              {"kind": "WORD", "text": "verify", "capitalized": "Verify"}
            ]
          }
        }
        """;
}
