using System.Collections.Immutable;
using System.Linq;
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
        ImmutableArray.Create(
            DiagnosticDescriptors.IgnoreAttributeOnInvalidParameterType,
            DiagnosticDescriptors.DefaultPathAttributeOnInvalidParameterType);

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
        var ignoreAttribute = parameter.GetAttributes()
            .FirstOrDefault(attr => IsAttributeType(attr, "Dagger", "IgnoreAttribute"));

        if (ignoreAttribute != null)
        {
            // Check if parameter type is Directory (only Directory is valid for [Ignore])
            if (!IsDirectoryType(parameter.Type))
            {
                var diagnostic = Diagnostic.Create(
                    DiagnosticDescriptors.IgnoreAttributeOnInvalidParameterType,
                    ignoreAttribute.ApplicationSyntaxReference?.GetSyntax().GetLocation() ?? parameter.Locations.FirstOrDefault(),
                    parameter.Name,
                    parameter.Type.Name);

                context.ReportDiagnostic(diagnostic);
            }
        }

        // Check if parameter has [DefaultPath] attribute
        var defaultPathAttribute = parameter.GetAttributes()
            .FirstOrDefault(attr => IsAttributeType(attr, "Dagger", "DefaultPathAttribute"));

        if (defaultPathAttribute != null)
        {
            // Check if parameter type is Directory or File (both are valid for [DefaultPath])
            if (!IsDirectoryOrFileType(parameter.Type))
            {
                var diagnostic = Diagnostic.Create(
                    DiagnosticDescriptors.DefaultPathAttributeOnInvalidParameterType,
                    defaultPathAttribute.ApplicationSyntaxReference?.GetSyntax().GetLocation() ?? parameter.Locations.FirstOrDefault(),
                    parameter.Name,
                    parameter.Type.Name);

                context.ReportDiagnostic(diagnostic);
            }
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
    /// Checks if the type is Dagger.Directory or Dagger.File
    /// </summary>
    private static bool IsDirectoryOrFileType(ITypeSymbol typeSymbol)
    {
        return (typeSymbol.Name == "Directory" || typeSymbol.Name == "File")
            && typeSymbol.ContainingNamespace?.ToString() == "Dagger";
    }

    /// <summary>
    /// Checks if an attribute matches a specific type and namespace
    /// </summary>
    private static bool IsAttributeType(AttributeData attribute, string namespaceName, string typeName)
    {
        var attributeClass = attribute.AttributeClass;
        return attributeClass?.Name == typeName
            && attributeClass.ContainingNamespace?.ToString() == namespaceName;
    }
}
