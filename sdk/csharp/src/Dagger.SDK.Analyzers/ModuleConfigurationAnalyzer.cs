using System.Collections.Immutable;
using Microsoft.CodeAnalysis;
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

        // DAGGER011 & DAGGER012: Check [Object] classes immediately (works live in IDE)
        context.RegisterSymbolAction(AnalyzeNamedType, SymbolKind.NamedType);

        // DAGGER013: Check project file name once per compilation (project-level diagnostic)
        context.RegisterCompilationAction(AnalyzeCompilation);
    }

    private void AnalyzeNamedType(SymbolAnalysisContext context)
    {
        var namedType = (INamedTypeSymbol)context.Symbol;

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

        // Try to find dagger.json from additional files
        var daggerConfig = DaggerJsonReader.FindDaggerJson(context.Options.AdditionalFiles);

        // DAGGER011: If no dagger.json, report immediately
        if (daggerConfig == null)
        {
            var projectDirectory = GetProjectDirectory(context.Compilation);
            var diagnostic = Diagnostic.Create(
                DiagnosticDescriptors.MissingDaggerJson,
                location,
                projectDirectory ?? "project directory"
            );
            context.ReportDiagnostic(diagnostic);
            return; // Don't check further if no config
        }

        var expectedClassName = DaggerJsonReader.FormatName(daggerConfig.Name);

        // DAGGER012: Check if THIS class matches the module name
        // If it doesn't match, we need to check if ANY other [Object] class matches
        if (namedType.Name != expectedClassName)
        {
            // Check if any other [Object] class in the compilation has the right name
            var hasMatchingClass = context
                .Compilation.GetSymbolsWithName(expectedClassName, SymbolFilter.Type)
                .OfType<INamedTypeSymbol>()
                .Any(t =>
                    t.GetAttributes()
                        .Any(attr =>
                            IsAttributeType(attr, "Dagger", "ObjectAttribute")
                            || IsAttributeType(attr, "Dagger", "Object")
                        )
                );

            // Only report if NO [Object] class has the correct name
            if (!hasMatchingClass)
            {
                var diagnostic = Diagnostic.Create(
                    DiagnosticDescriptors.ModuleClassNameMismatch,
                    location,
                    expectedClassName,
                    daggerConfig.Name
                );
                context.ReportDiagnostic(diagnostic);
            }
        }
    }

    private void AnalyzeCompilation(CompilationAnalysisContext context)
    {
        // Try to find dagger.json from additional files
        var daggerConfig = DaggerJsonReader.FindDaggerJson(context.Options.AdditionalFiles);
        if (daggerConfig == null)
        {
            return; // No dagger.json, can't check project file name
        }

        var expectedClassName = DaggerJsonReader.FormatName(daggerConfig.Name);
        var assemblyName = context.Compilation.AssemblyName;

        // DAGGER013: Check project file name matches expected module name
        if (!string.IsNullOrEmpty(assemblyName) && assemblyName != expectedClassName)
        {
            // Find the first [Object] class location for the diagnostic
            var objectClass = context
                .Compilation.GetSymbolsWithName(_ => true, SymbolFilter.Type)
                .OfType<INamedTypeSymbol>()
                .FirstOrDefault(symbol =>
                    symbol
                        .GetAttributes()
                        .Any(attr =>
                            IsAttributeType(attr, "Dagger", "ObjectAttribute")
                            || IsAttributeType(attr, "Dagger", "Object")
                        )
                );

            if (objectClass != null)
            {
                var location = objectClass.Locations.FirstOrDefault() ?? Location.None;
                var diagnostic = Diagnostic.Create(
                    DiagnosticDescriptors.ProjectFileNameMismatch,
                    location,
                    expectedClassName,
                    daggerConfig.Name,
                    assemblyName
                );
                context.ReportDiagnostic(diagnostic);
            }
        }
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
                var directory = Path.GetDirectoryName(filePath);
                // Normalize to forward slashes for consistent cross-platform diagnostics
                return NormalizePath(directory);
            }
        }

        return null;
    }

    /// <summary>
    /// Normalizes path separators to forward slashes for consistent cross-platform display.
    /// </summary>
    private static string? NormalizePath(string? path)
    {
        return path?.Replace('\\', '/');
    }
}
