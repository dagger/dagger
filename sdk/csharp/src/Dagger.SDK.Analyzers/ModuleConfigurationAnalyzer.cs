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

        // Collect all [Object] classes and check for module root in compilation-end
        var objectClasses = new List<INamedTypeSymbol>();
        var objectClassesLock = new object();

        // Register symbol action to collect [Object] classes
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

            if (hasObjectAttribute)
            {
                lock (objectClassesLock)
                {
                    objectClasses.Add(namedType);
                }

                // If no dagger.json, report immediately
                if (daggerConfig == null)
                {
                    var location = namedType.Locations.FirstOrDefault();
                    if (location != null)
                    {
                        var projectDirectory = GetProjectDirectory(symbolContext.Compilation);
                        var diagnostic = Diagnostic.Create(
                            DiagnosticDescriptors.MissingDaggerJson,
                            location,
                            projectDirectory ?? "project directory"
                        );
                        symbolContext.ReportDiagnostic(diagnostic);
                    }
                }
            }
        }, SymbolKind.NamedType);

        // Use compilation-end to determine if module root exists, then report on all classes if missing
        context.RegisterCompilationEndAction(compilationContext =>
        {
            if (daggerConfig == null || objectClasses.Count == 0)
            {
                return;
            }

            var expectedClassName = DaggerJsonReader.FormatName(daggerConfig.Name);

            // Order classes by location for deterministic reporting
            var orderedObjectClasses = objectClasses
                .Where(cls => cls.Locations.FirstOrDefault() != null)
                .OrderBy(cls => cls.Locations[0].SourceTree?.FilePath ?? string.Empty)
                .ThenBy(cls => cls.Locations[0].SourceSpan.Start)
                .ToList();

            // Check if at least one [Object] class matches the expected module root name
            var hasModuleRoot = orderedObjectClasses.Any(cls => cls.Name == expectedClassName);

            if (!hasModuleRoot)
            {
                // No class matches - warn on all [Object] classes
                foreach (var objectClass in orderedObjectClasses)
                {
                    var location = objectClass.Locations.FirstOrDefault();
                    if (location != null)
                    {
                        var diagnostic = Diagnostic.Create(
                            DiagnosticDescriptors.ModuleClassNameMismatch,
                            location,
                            expectedClassName,
                            daggerConfig.Name
                        );
                        compilationContext.ReportDiagnostic(diagnostic);
                    }
                }
            }

            // Check project file name (report once on first [Object] class)
            var assemblyName = compilationContext.Compilation.AssemblyName;
            if (!string.IsNullOrEmpty(assemblyName) && assemblyName != expectedClassName)
            {
                var firstObjectClass = orderedObjectClasses.FirstOrDefault();
                var location = firstObjectClass?.Locations.FirstOrDefault();
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
