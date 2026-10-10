using System;
using System.Collections.Generic;
using System.Linq;
using System.Text;
using System.Text.Json;
using Dagger.SDK.SourceGenerator.Code;
using Dagger.SDK.SourceGenerator.Types;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.Text;

namespace Dagger.SDK.SourceGenerator;

[Generator(LanguageNames.CSharp)]
public class SourceGenerator(CodeGenerator codeGenerator) : IIncrementalGenerator
{
    public static readonly Diagnostic NoSchemaFileFound = Diagnostic.Create(
        new DiagnosticDescriptor(
            id: "DAG001",
            title: "No introspection.json file found",
            messageFormat: "No introspection.json file was found in the additional files. The source generator will not generate any code.",
            category: "Dagger.SDK.SourceGenerator",
            DiagnosticSeverity.Warning,
            isEnabledByDefault: true
        ),
        location: null
    );

    public static readonly Diagnostic FailedToReadSchemaFile = Diagnostic.Create(
        new DiagnosticDescriptor(
            id: "DAG002",
            title: "Failed to read introspection.json file",
            messageFormat: "Failed to read introspection.json file. The source generator will not generate any code.",
            category: "Dagger.SDK.SourceGenerator",
            DiagnosticSeverity.Warning,
            isEnabledByDefault: true
        ),
        location: null
    );

    public static Diagnostic FailedToParseSchemaFile =>
        Diagnostic.Create(
            new DiagnosticDescriptor(
                id: "DAG003",
                title: "Failed to parse introspection.json file",
                messageFormat: "Failed to parse introspection.json file. The source generator will not generate any code.",
                category: "Dagger.SDK.SourceGenerator",
                DiagnosticSeverity.Error,
                isEnabledByDefault: true
            ),
            location: null
        );

    public static Diagnostic FailedToParseNamesFile =>
        Diagnostic.Create(
            new DiagnosticDescriptor(
                id: "DAG005",
                title: "Failed to read names.json file",
                messageFormat: "Failed to read the names.json file next to introspection.json. The source generator will not generate any code.",
                category: "Dagger.SDK.SourceGenerator",
                DiagnosticSeverity.Error,
                isEnabledByDefault: true
            ),
            location: null
        );

    public SourceGenerator()
        : this(new CodeGenerator(new CodeRenderer())) { }

    public void Initialize(IncrementalGeneratorInitializationContext context)
    {
        IncrementalValuesProvider<AdditionalText> additionalText = context.AdditionalTextsProvider;
        IncrementalValuesProvider<AdditionalText> schemaFiles = additionalText.Where(static x =>
            x.Path.EndsWith("introspection.json")
        );
        IncrementalValuesProvider<SourceText?> sourceTexts = schemaFiles.Select(
            (text, ct) => text.GetText(cancellationToken: ct)
        );
        // The names the engine formatted for the schema's names, written next
        // to introspection.json by `codegen introspect --names-out` (see
        // Namer). Without it, names get the legacy conversion.
        IncrementalValuesProvider<SourceText?> namesTexts = additionalText
            .Where(static x => IsNamesFile(x.Path))
            .Select((text, ct) => text.GetText(cancellationToken: ct));
        var items = sourceTexts.Collect().Combine(namesTexts.Collect());

        context.RegisterSourceOutput(
            items,
            (spc, input) =>
            {
                var (sources, namesSources) = input;
                if (sources.Length == 0)
                {
                    spc.ReportDiagnostic(NoSchemaFileFound);
                    return;
                }

                if (sources.Length != 1)
                {
                    spc.ReportDiagnostic(FailedToReadSchemaFile);
                    return;
                }

                if (sources[0] is null)
                {
                    spc.ReportDiagnostic(FailedToReadSchemaFile);
                    return;
                }

                Dictionary<string, Dictionary<string, string>>? names = null;
                if (namesSources.Length > 0)
                {
                    if (namesSources.Length != 1 || namesSources[0] is null)
                    {
                        spc.ReportDiagnostic(FailedToParseNamesFile);
                        return;
                    }
                    try
                    {
                        names = Namer.ParseNames(namesSources[0]!.ToString());
                    }
                    catch (JsonException)
                    {
                        spc.ReportDiagnostic(FailedToParseNamesFile);
                        return;
                    }
                }

                try
                {
                    Introspection introspection = JsonSerializer.Deserialize<Introspection>(
                        sources[0]!.ToString()
                    )!;
                    string code = codeGenerator.Generate(introspection, names);
                    spc.AddSource("Dagger.SDK.g.cs", SourceText.From(code, Encoding.UTF8));
                }
                catch (JsonException)
                {
                    spc.ReportDiagnostic(FailedToParseSchemaFile);
                }
                catch (Exception ex)
                {
                    spc.ReportDiagnostic(FailedToGenerateCode(ex));
                }
            }
        );
    }

    private static bool IsNamesFile(string path) =>
        path == "names.json" || path.EndsWith("/names.json") || path.EndsWith("\\names.json");

    private static Diagnostic FailedToGenerateCode(Exception ex)
    {
        return Diagnostic.Create(
            new DiagnosticDescriptor(
                id: "DAG004",
                title: "Failed to generate SDK code",
                messageFormat: "Failed to generate code. The source generator will not generate any code. Cause: {0}",
                category: "Dagger.SDK.SourceGenerator",
                DiagnosticSeverity.Error,
                isEnabledByDefault: true
            ),
            location: null,
            messageArgs: ex.Message
        );
    }
}
