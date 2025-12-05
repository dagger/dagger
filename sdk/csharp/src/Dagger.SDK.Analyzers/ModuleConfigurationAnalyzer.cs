using System.Collections.Generic;
using System.Collections.Immutable;
using System.IO;
using System.Linq;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.CSharp.Syntax;
using Microsoft.CodeAnalysis.Diagnostics;

namespace Dagger.SDK.Analyzers;

[DiagnosticAnalyzer(LanguageNames.CSharp)]
public class ModuleConfigurationAnalyzer : DiagnosticAnalyzer
{
    public override ImmutableArray<DiagnosticDescriptor> SupportedDiagnostics =>
        [
            DiagnosticDescriptors.MissingDaggerJson,
            DiagnosticDescriptors.ModuleClassNameMismatch,
            DiagnosticDescriptors.ProjectFileNameMismatch,
        ];

    public override void Initialize(AnalysisContext context)
    {
        context.ConfigureGeneratedCodeAnalysis(GeneratedCodeAnalysisFlags.None);
        context.EnableConcurrentExecution();
        context.RegisterCompilationStartAction(AnalyzeCompilation);
    }

    private void AnalyzeCompilation(CompilationStartAnalysisContext context)
    {
        // Try to find dagger.json from additional files
        var daggerConfig = DaggerJsonReader.FindDaggerJson(
            context.Options.AdditionalFiles
        );

        // Register symbol action to check each [Object] class immediately
        context.RegisterSymbolAction(symbolContext =>
        {
            var namedType = (INamedTypeSymbol)symbolContext.Symbol;
            
            // Check if class has [Object] attribute
            var hasObjectAttribute = namedType
                .GetAttributes()
                .Any(attr =>
                    IsAttributeType(attr, "Dagger", "ObjectAttribute")
                    || IsAttributeType(attr, "Dagger", "Object")
                );

            if (!hasObjectAttribute)
                return;

            var location = namedType.Locations.FirstOrDefault();
            if (location == null)
                return;

            // DAGGER011: If no dagger.json, report immediately
            if (daggerConfig == null)
            {
                var projectDirectory = GetProjectDirectory(symbolContext.Compilation);
                var diagnostic = Diagnostic.Create(
                    DiagnosticDescriptors.MissingDaggerJson,
                    location,
                    projectDirectory ?? "project directory"
                );
                symbolContext.ReportDiagnostic(diagnostic);
                return; // Don't check further if no config
            }

            // DAGGER012: Check if this class name matches the expected module name
            var expectedClassName = DaggerJsonReader.FormatName(daggerConfig.Name);
            if (namedType.Name != expectedClassName)
            {
                var diagnostic = Diagnostic.Create(
                    DiagnosticDescriptors.ModuleClassNameMismatch,
                    location,
                    expectedClassName,
                    daggerConfig.Name
                );
                symbolContext.ReportDiagnostic(diagnostic);
            }
        }, SymbolKind.NamedType);

        // DAGGER013: Check project file name at compilation-end (only needs to run once)
        context.RegisterCompilationEndAction(compilationContext =>
        {
            if (daggerConfig == null)
                return;

            var expectedClassName = DaggerJsonReader.FormatName(daggerConfig.Name);
            var assemblyName = compilationContext.Compilation.AssemblyName;
            
            if (string.IsNullOrEmpty(assemblyName) || assemblyName == expectedClassName)
                return;

            // Find any [Object] class to report on
            var objectClass = compilationContext.Compilation.GetSymbolsWithName(
                _ => true,
                SymbolFilter.Type
            )
            .OfType<INamedTypeSymbol>()
            .FirstOrDefault(t => t.GetAttributes().Any(attr =>
                IsAttributeType(attr, "Dagger", "ObjectAttribute")
                || IsAttributeType(attr, "Dagger", "Object")
            ));

            var location = objectClass?.Locations.FirstOrDefault();
            if (location != null)
            {
                var diagnostic = Diagnostic.Create(
                    DiagnosticDescriptors.ProjectFileNameMismatch,
                    location,
                    expectedClassName,
                    daggerConfig.Name,
                    assemblyName
                );
                compilationContext.ReportDiagnostic(diagnostic);
            }
        });
    }

    private static bool IsAttributeType(
        AttributeData attribute,
        string namespaceName,
        string typeName
    )
    {
        var attrClass = attribute.AttributeClass;
        return attrClass != null
            && attrClass.Name == typeName
            && attrClass.ContainingNamespace?.ToString() == namespaceName;
    }

    private static string? GetProjectDirectory(Compilation compilation)
    {
        // Try to get project directory from syntax trees
        var firstTree = compilation.SyntaxTrees.FirstOrDefault();
        if (firstTree?.FilePath != null)
        {
            var filePath = firstTree.FilePath;
            if (!string.IsNullOrEmpty(filePath))
            {
                return Path.GetDirectoryName(filePath);
            }
        }

        return null;
    }
}
