using System.Collections.Immutable;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.CSharp.Syntax;
using Microsoft.CodeAnalysis.Diagnostics;

namespace Dagger.SDK.Analyzers;

[DiagnosticAnalyzer(LanguageNames.CSharp)]
public class DaggerDirectoryAnalyzer : DiagnosticAnalyzer
{
    public override ImmutableArray<DiagnosticDescriptor> SupportedDiagnostics =>
        [
            DiagnosticDescriptors.DirectoryParameterShouldHaveDefaultPath,
            DiagnosticDescriptors.DirectoryParameterShouldHaveIgnore,
        ];

    public override void Initialize(AnalysisContext context)
    {
        context.ConfigureGeneratedCodeAnalysis(GeneratedCodeAnalysisFlags.None);
        context.EnableConcurrentExecution();
        context.RegisterSyntaxNodeAction(AnalyzeParameter, SyntaxKind.Parameter);
    }

    private void AnalyzeParameter(SyntaxNodeAnalysisContext context)
    {
        var parameter = (ParameterSyntax)context.Node;
        var parameterSymbol = context.SemanticModel.GetDeclaredSymbol(parameter);

        if (parameterSymbol == null)
            return;

        // Check if parameter type is Dagger.Directory
        if (!IsDirectoryType(parameterSymbol.Type))
            return;

        // Check if parameter is in a method with [Function] attribute
        if (!IsInFunctionMethod(parameter, context))
            return;

        var attributes = parameterSymbol.GetAttributes();

        // Check for [DefaultPath] attribute
        var hasDefaultPath = attributes.Any(attr =>
            IsAttributeType(attr, "Dagger", "DefaultPathAttribute")
            || IsAttributeType(attr, "Dagger", "DefaultPath")
        );

        // Check for [Ignore] attribute
        var hasIgnore = attributes.Any(attr =>
            IsAttributeType(attr, "Dagger", "IgnoreAttribute")
            || IsAttributeType(attr, "Dagger", "Ignore")
        );

        // Suggest [DefaultPath] if not present
        if (!hasDefaultPath)
        {
            var diagnostic = Diagnostic.Create(
                DiagnosticDescriptors.DirectoryParameterShouldHaveDefaultPath,
                parameter.Identifier.GetLocation(),
                parameterSymbol.Name
            );
            context.ReportDiagnostic(diagnostic);
        }

        // Suggest [Ignore] if not present
        if (!hasIgnore)
        {
            var diagnostic = Diagnostic.Create(
                DiagnosticDescriptors.DirectoryParameterShouldHaveIgnore,
                parameter.Identifier.GetLocation(),
                parameterSymbol.Name
            );
            context.ReportDiagnostic(diagnostic);
        }
    }

    private static bool IsDirectoryType(ITypeSymbol typeSymbol)
    {
        return typeSymbol.Name == "Directory"
            && typeSymbol.ContainingNamespace?.ToString() == "Dagger";
    }

    private static bool IsInFunctionMethod(
        ParameterSyntax parameter,
        SyntaxNodeAnalysisContext context
    )
    {
        // Walk up to find the containing method
        var method = parameter.Ancestors().OfType<MethodDeclarationSyntax>().FirstOrDefault();
        if (method == null)
            return false;

        var methodSymbol = context.SemanticModel.GetDeclaredSymbol(method);
        if (methodSymbol == null)
            return false;

        // Check if method has [Function] attribute
        return methodSymbol
            .GetAttributes()
            .Any(attr =>
                IsAttributeType(attr, "Dagger", "FunctionAttribute")
                || IsAttributeType(attr, "Dagger", "Function")
            );
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
}
