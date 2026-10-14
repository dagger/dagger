using System;
using System.Collections.Generic;
using System.Collections.Immutable;
using System.IO;
using System.Linq;
using System.Reflection;
using System.Text.Json;
using Dagger.SDK.SourceGenerator.Code;
using Dagger.SDK.SourceGenerator.Types;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;

namespace Dagger.SDK.SourceGenerator.Tests.Utils;

/// <summary>
/// Files embedded into the test assembly (see the test project).
/// </summary>
public static class Fixtures
{
    private const string RuntimePrefix = "Runtime/";

    // The implicit usings of the Dagger.SDK project.
    private const string RuntimeGlobalUsings = """
        global using System;
        global using System.Collections.Generic;
        global using System.IO;
        global using System.Linq;
        global using System.Net.Http;
        global using System.Threading;
        global using System.Threading.Tasks;
        """;

    public static string Read(string name)
    {
        using var stream =
            Assembly.GetExecutingAssembly().GetManifestResourceStream(name)
            ?? throw new InvalidOperationException($"Missing embedded fixture {name}.");
        using var reader = new StreamReader(stream);
        return reader.ReadToEnd();
    }

    /// <summary>
    /// The core schema JSON, descriptions stripped. Fixtures/generate.go
    /// regenerates it from docs/docs-graphql/schema.graphqls.
    /// </summary>
    public static string CoreSchema => Read("core-schema.json");

    /// <summary>
    /// The names file for the core schema, in the formats the source
    /// generator reads, as the engine formats them. Fixtures/generate.go
    /// regenerates it with the engine's naming package.
    /// </summary>
    public static Dictionary<string, Dictionary<string, string>> CoreNames() =>
        Namer.ParseNames(Read("core-names.json"));

    public static Introspection Parse(string json) =>
        JsonSerializer.Deserialize<Introspection>(json)!;

    /// <summary>
    /// Compile generated code together with the SDK runtime sources, and
    /// return the errors.
    /// </summary>
    public static ImmutableArray<Diagnostic> CompileWithRuntime(string generated)
    {
        var options = new CSharpParseOptions(LanguageVersion.Latest);
        var assembly = Assembly.GetExecutingAssembly();
        var trees = new List<SyntaxTree>
        {
            CSharpSyntaxTree.ParseText(generated, options, "Dagger.SDK.g.cs"),
            CSharpSyntaxTree.ParseText(RuntimeGlobalUsings, options, "GlobalUsings.cs"),
        };
        trees.AddRange(
            assembly
                .GetManifestResourceNames()
                .Where(name => name.StartsWith(RuntimePrefix))
                .Select(name => CSharpSyntaxTree.ParseText(Read(name), options, name))
        );

        var frameworkDir = Path.GetDirectoryName(typeof(object).Assembly.Location)!;
        var references = ((string)AppContext.GetData("TRUSTED_PLATFORM_ASSEMBLIES")!)
            .Split(Path.PathSeparator)
            .Where(path => Path.GetDirectoryName(path) == frameworkDir)
            .Select(path => MetadataReference.CreateFromFile(path));

        var compilation = CSharpCompilation.Create(
            "Dagger.SDK",
            trees,
            references,
            new CSharpCompilationOptions(
                OutputKind.DynamicallyLinkedLibrary,
                nullableContextOptions: NullableContextOptions.Enable
            )
        );
        return compilation
            .GetDiagnostics()
            .Where(diagnostic => diagnostic.Severity == DiagnosticSeverity.Error)
            .ToImmutableArray();
    }
}
