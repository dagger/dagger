using System.Collections.Immutable;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.Diagnostics;

namespace Dagger.SDK.Analyzers;

/// <summary>
/// Analyzer that ensures [Ignore] and [DefaultPath] attributes are only applied to valid parameter types.
/// </summary>
[DiagnosticAnalyzer(LanguageNames.CSharp)]
public class IgnoreAttributeAnalyzer : DiagnosticAnalyzer
{
    public override ImmutableArray<DiagnosticDescriptor> SupportedDiagnostics =>
        [
            DiagnosticDescriptors.IgnoreAttributeOnInvalidParameterType,
            DiagnosticDescriptors.DefaultPathAttributeOnInvalidParameterType,
            DiagnosticDescriptors.DefaultAddressAttributeOnInvalidParameterType,
        ];

    public override void Initialize(AnalysisContext context)
    {
        context.ConfigureGeneratedCodeAnalysis(GeneratedCodeAnalysisFlags.None);
        context.EnableConcurrentExecution();

        context.RegisterSymbolAction(AnalyzeParameter, SymbolKind.Parameter);
    }

    private static void AnalyzeParameter(SymbolAnalysisContext context)
    {
        var parameter = (IParameterSymbol)context.Symbol;

        // Check if parameter has [Ignore] attribute
        var ignoreAttribute = parameter
            .GetAttributes()
            .FirstOrDefault(attr => IsAttributeType(attr, "Dagger", "IgnoreAttribute"));

        if (ignoreAttribute != null)
        {
            // Check if parameter type is Directory (only Directory is valid for [Ignore])
            if (!IsDirectoryType(parameter.Type))
            {
                var diagnostic = Diagnostic.Create(
                    DiagnosticDescriptors.IgnoreAttributeOnInvalidParameterType,
                    ignoreAttribute.ApplicationSyntaxReference?.GetSyntax().GetLocation()
                        ?? parameter.Locations.FirstOrDefault(),
                    parameter.Name,
                    parameter.Type.Name
                );

                context.ReportDiagnostic(diagnostic);
            }
        }

        // Check if parameter has [DefaultPath] attribute
        var defaultPathAttribute = parameter
            .GetAttributes()
            .FirstOrDefault(attr => IsAttributeType(attr, "Dagger", "DefaultPathAttribute"));

        if (defaultPathAttribute != null)
        {
            // Check if parameter type supports contextual path resolution
            // (Directory, File, and the git types GitRepository/GitRef)
            if (!IsDefaultPathCapableType(parameter.Type))
            {
                var diagnostic = Diagnostic.Create(
                    DiagnosticDescriptors.DefaultPathAttributeOnInvalidParameterType,
                    defaultPathAttribute.ApplicationSyntaxReference?.GetSyntax().GetLocation()
                        ?? parameter.Locations.FirstOrDefault(),
                    parameter.Name,
                    parameter.Type.Name
                );

                context.ReportDiagnostic(diagnostic);
            }
        }

        // Check if parameter has [DefaultAddress] attribute
        var defaultAddressAttribute = parameter
            .GetAttributes()
            .FirstOrDefault(attr => IsAttributeType(attr, "Dagger", "DefaultAddressAttribute"));

        if (
            defaultAddressAttribute != null
            && parameter
                .Type.WithNullableAnnotation(NullableAnnotation.NotAnnotated)
                .ToDisplayString() != "Dagger.Container"
        )
        {
            context.ReportDiagnostic(
                Diagnostic.Create(
                    DiagnosticDescriptors.DefaultAddressAttributeOnInvalidParameterType,
                    defaultAddressAttribute.ApplicationSyntaxReference?.GetSyntax().GetLocation()
                        ?? parameter.Locations.FirstOrDefault(),
                    parameter.Name,
                    parameter.Type.Name
                )
            );
        }
    }

    /// <summary>
    /// Checks if the type is Dagger.Directory
    /// </summary>
    private static bool IsDirectoryType(ITypeSymbol typeSymbol)
    {
        return typeSymbol.Name == "Directory"
            && typeSymbol.ContainingNamespace?.ToString() == "Dagger";
    }

    /// <summary>
    /// Checks if the type supports [DefaultPath] contextual resolution:
    /// Dagger.Directory, Dagger.File, Dagger.GitRepository or Dagger.GitRef
    /// (the engine resolves contextual git arguments from the module's repo).
    /// </summary>
    private static bool IsDefaultPathCapableType(ITypeSymbol typeSymbol)
    {
        return typeSymbol.Name is "Directory" or "File" or "GitRepository" or "GitRef"
            && typeSymbol.ContainingNamespace?.ToString() == "Dagger";
    }

    /// <summary>
    /// Checks if an attribute matches a specific type and namespace
    /// </summary>
    private static bool IsAttributeType(
        AttributeData attribute,
        string namespaceName,
        string typeName
    )
    {
        var attributeClass = attribute.AttributeClass;
        return attributeClass?.Name == typeName
            && attributeClass.ContainingNamespace?.ToString() == namespaceName;
    }
}
