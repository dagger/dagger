using System.Collections.Immutable;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.CSharp.Syntax;
using Microsoft.CodeAnalysis.Diagnostics;

namespace Dagger.SDK.Analyzers;

[DiagnosticAnalyzer(LanguageNames.CSharp)]
public class CheckAttributeAnalyzer : DiagnosticAnalyzer
{
    public override ImmutableArray<DiagnosticDescriptor> SupportedDiagnostics =>
        [
            DiagnosticDescriptors.CheckFunctionWithRequiredParameters,
            DiagnosticDescriptors.CheckFunctionInvalidReturnType,
        ];

    public override void Initialize(AnalysisContext context)
    {
        context.ConfigureGeneratedCodeAnalysis(GeneratedCodeAnalysisFlags.None);
        context.EnableConcurrentExecution();
        context.RegisterSyntaxNodeAction(AnalyzeMethodDeclaration, SyntaxKind.MethodDeclaration);
    }

    private void AnalyzeMethodDeclaration(SyntaxNodeAnalysisContext context)
    {
        var methodDeclaration = (MethodDeclarationSyntax)context.Node;
        var methodSymbol = context.SemanticModel.GetDeclaredSymbol(methodDeclaration);

        if (methodSymbol == null)
            return;

        // Check if method has [Check] attribute
        var hasCheckAttribute = methodSymbol
            .GetAttributes()
            .Any(attr =>
                IsAttributeType(attr, "Dagger", "CheckAttribute")
                || IsAttributeType(attr, "Dagger", "Check")
            );

        if (!hasCheckAttribute)
            return;

        // Check if function has required parameters
        var requiredParams = new List<string>();
        foreach (var param in methodSymbol.Parameters)
        {
            // Skip if parameter is optional (has default value)
            if (param.HasExplicitDefaultValue)
                continue;

            // Skip if parameter type is nullable reference type
            if (param.Type.NullableAnnotation == NullableAnnotation.Annotated)
                continue;

            // Skip if parameter type is nullable value type (e.g., int?)
            if (
                param.Type is INamedTypeSymbol namedType
                && namedType.OriginalDefinition.SpecialType == SpecialType.System_Nullable_T
            )
                continue;

            // Check if parameter has [DefaultPath] attribute (contextual optional)
            var hasDefaultPath = param
                .GetAttributes()
                .Any(attr =>
                    IsAttributeType(attr, "Dagger", "DefaultPathAttribute")
                    || IsAttributeType(attr, "Dagger", "DefaultPath")
                );

            if (hasDefaultPath)
                continue;

            // This is a required parameter
            requiredParams.Add(param.Name);
        }

        // Report diagnostic if there are required parameters
        if (requiredParams.Count > 0)
        {
            var paramList = string.Join(", ", requiredParams.Select(p => $"'{p}'"));
            var diagnostic = Diagnostic.Create(
                DiagnosticDescriptors.CheckFunctionWithRequiredParameters,
                methodDeclaration.Identifier.GetLocation(),
                methodSymbol.Name,
                paramList
            );
            context.ReportDiagnostic(diagnostic);
        }

        // Validate return type
        if (!IsValidCheckReturnType(methodSymbol.ReturnType))
        {
            var diagnostic = Diagnostic.Create(
                DiagnosticDescriptors.CheckFunctionInvalidReturnType,
                methodDeclaration.ReturnType.GetLocation(),
                methodSymbol.Name,
                methodSymbol.ReturnType.ToDisplayString()
            );
            context.ReportDiagnostic(diagnostic);
        }
    }

    private static bool IsValidCheckReturnType(ITypeSymbol returnType)
    {
        // void is valid
        if (returnType.SpecialType == SpecialType.System_Void)
            return true;

        // Container is valid
        if (IsContainerType(returnType))
            return true;

        // Task (non-generic) is valid
        if (IsTaskType(returnType, out var taskTypeArg))
        {
            // Task with no type argument is valid
            if (taskTypeArg == null)
                return true;

            // Task<Container> is valid
            if (IsContainerType(taskTypeArg))
                return true;
        }

        return false;
    }

    private static bool IsContainerType(ITypeSymbol type)
    {
        var fullName = type.ToDisplayString();
        return fullName == "Dagger.Container";
    }

    private static bool IsTaskType(ITypeSymbol type, out ITypeSymbol? typeArgument)
    {
        typeArgument = null;

        if (type is not INamedTypeSymbol namedType)
            return false;

        var fullName = namedType.OriginalDefinition.ToDisplayString();

        // Check for Task or Task<T>
        if (fullName is "System.Threading.Tasks.Task" or "System.Threading.Tasks.Task<TResult>")
        {
            // Get type argument if it's Task<T>
            if (namedType.TypeArguments.Length == 1)
            {
                typeArgument = namedType.TypeArguments[0];
            }
            return true;
        }

        return false;
    }

    private static bool IsAttributeType(
        AttributeData attribute,
        string namespaceName,
        string attributeName
    )
    {
        if (attribute.AttributeClass == null)
            return false;

        var fullName = attribute.AttributeClass.ToDisplayString();
        return fullName == $"{namespaceName}.{attributeName}"
            || fullName == $"{namespaceName}.{attributeName}Attribute";
    }
}
