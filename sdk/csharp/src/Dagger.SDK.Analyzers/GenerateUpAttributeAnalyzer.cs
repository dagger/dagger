using System.Collections.Immutable;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.CSharp.Syntax;
using Microsoft.CodeAnalysis.Diagnostics;

namespace Dagger.SDK.Analyzers;

/// <summary>
/// Validates functions marked with [Generate] (DAGGER024, DAGGER025) and
/// [Up] (DAGGER026, DAGGER027). The engine runs both without arguments and
/// requires Changeset and Service return types respectively.
/// </summary>
[DiagnosticAnalyzer(LanguageNames.CSharp)]
public class GenerateUpAttributeAnalyzer : DiagnosticAnalyzer
{
    public override ImmutableArray<DiagnosticDescriptor> SupportedDiagnostics =>
        [
            DiagnosticDescriptors.GenerateFunctionWithRequiredParameters,
            DiagnosticDescriptors.GenerateFunctionInvalidReturnType,
            DiagnosticDescriptors.UpFunctionWithRequiredParameters,
            DiagnosticDescriptors.UpFunctionInvalidReturnType,
        ];

    public override void Initialize(AnalysisContext context)
    {
        context.ConfigureGeneratedCodeAnalysis(GeneratedCodeAnalysisFlags.None);
        context.EnableConcurrentExecution();
        context.RegisterSyntaxNodeAction(AnalyzeMethodDeclaration, SyntaxKind.MethodDeclaration);
    }

    private static void AnalyzeMethodDeclaration(SyntaxNodeAnalysisContext context)
    {
        var methodDeclaration = (MethodDeclarationSyntax)context.Node;
        var methodSymbol = context.SemanticModel.GetDeclaredSymbol(methodDeclaration);
        if (methodSymbol == null)
            return;

        var attributes = methodSymbol.GetAttributes();

        if (attributes.Any(attr => IsAttributeType(attr, "Generate")))
        {
            Validate(
                context,
                methodDeclaration,
                methodSymbol,
                "Dagger.Changeset",
                DiagnosticDescriptors.GenerateFunctionWithRequiredParameters,
                DiagnosticDescriptors.GenerateFunctionInvalidReturnType
            );
        }

        if (attributes.Any(attr => IsAttributeType(attr, "Up")))
        {
            Validate(
                context,
                methodDeclaration,
                methodSymbol,
                "Dagger.Service",
                DiagnosticDescriptors.UpFunctionWithRequiredParameters,
                DiagnosticDescriptors.UpFunctionInvalidReturnType
            );
        }
    }

    private static void Validate(
        SyntaxNodeAnalysisContext context,
        MethodDeclarationSyntax methodDeclaration,
        IMethodSymbol methodSymbol,
        string expectedReturnType,
        DiagnosticDescriptor requiredParametersDescriptor,
        DiagnosticDescriptor invalidReturnTypeDescriptor
    )
    {
        var requiredParams = methodSymbol
            .Parameters.Where(IsRequiredParameter)
            .Select(p => $"'{p.Name}'")
            .ToList();

        if (requiredParams.Count > 0)
        {
            context.ReportDiagnostic(
                Diagnostic.Create(
                    requiredParametersDescriptor,
                    methodDeclaration.Identifier.GetLocation(),
                    methodSymbol.Name,
                    string.Join(", ", requiredParams)
                )
            );
        }

        if (!ReturnsType(methodSymbol.ReturnType, expectedReturnType))
        {
            context.ReportDiagnostic(
                Diagnostic.Create(
                    invalidReturnTypeDescriptor,
                    methodDeclaration.ReturnType.GetLocation(),
                    methodSymbol.Name,
                    methodSymbol.ReturnType.ToDisplayString()
                )
            );
        }
    }

    private static bool IsRequiredParameter(IParameterSymbol param)
    {
        if (param.HasExplicitDefaultValue)
            return false;

        if (param.Type.NullableAnnotation == NullableAnnotation.Annotated)
            return false;

        if (
            param.Type is INamedTypeSymbol namedType
            && namedType.OriginalDefinition.SpecialType == SpecialType.System_Nullable_T
        )
            return false;

        if (param.Type.ToDisplayString() == "System.Threading.CancellationToken")
            return false;

        return !param
            .GetAttributes()
            .Any(attr =>
                IsAttributeType(attr, "DefaultPath") || IsAttributeType(attr, "DefaultAddress")
            );
    }

    private static bool ReturnsType(ITypeSymbol returnType, string expected)
    {
        if (returnType.ToDisplayString() == expected)
            return true;

        return returnType is INamedTypeSymbol namedType
            && namedType.TypeArguments.Length == 1
            && namedType.OriginalDefinition.ToDisplayString()
                is "System.Threading.Tasks.Task<TResult>"
                    or "System.Threading.Tasks.ValueTask<TResult>"
            && namedType.TypeArguments[0].ToDisplayString() == expected;
    }

    private static bool IsAttributeType(AttributeData attribute, string attributeName)
    {
        var fullName = attribute.AttributeClass?.ToDisplayString();
        return fullName == $"Dagger.{attributeName}"
            || fullName == $"Dagger.{attributeName}Attribute";
    }
}
