using System.Collections.Immutable;
using System.Linq;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.CSharp.Syntax;
using Microsoft.CodeAnalysis.Diagnostics;
using static Dagger.SDK.Analyzers.DiagnosticDescriptors;

namespace Dagger.SDK.Analyzers;

[DiagnosticAnalyzer(LanguageNames.CSharp)]
public class CustomReturnTypeAnalyzer : DiagnosticAnalyzer
{
    public override ImmutableArray<DiagnosticDescriptor> SupportedDiagnostics =>
        [CustomReturnTypeMissingObjectAttribute];

    public override void Initialize(AnalysisContext context)
    {
        context.ConfigureGeneratedCodeAnalysis(GeneratedCodeAnalysisFlags.None);
        context.EnableConcurrentExecution();
        context.RegisterSyntaxNodeAction(AnalyzeMethod, SyntaxKind.MethodDeclaration);
    }

    private void AnalyzeMethod(SyntaxNodeAnalysisContext context)
    {
        var methodDeclaration = (MethodDeclarationSyntax)context.Node;
        var methodSymbol = context.SemanticModel.GetDeclaredSymbol(methodDeclaration);

        if (methodSymbol == null)
            return;

        // Only analyze methods with [Function] attribute
        var hasFunctionAttribute = methodSymbol
            .GetAttributes()
            .Any(static attr =>
                IsAttributeType(attr, "Dagger", "FunctionAttribute")
                || IsAttributeType(attr, "Dagger", "Function")
            );

        if (!hasFunctionAttribute)
        {
            return;
        }

        var returnType = methodSymbol.ReturnType;

        // Unwrap Task<T> and ValueTask<T>
        if (returnType is INamedTypeSymbol { IsGenericType: true } namedType)
        {
            var typeName = namedType.OriginalDefinition.ToDisplayString();
            if (
                typeName == "System.Threading.Tasks.Task<TResult>"
                || typeName == "System.Threading.Tasks.ValueTask<TResult>"
            )
            {
                returnType = namedType.TypeArguments[0];
            }
        }

        // Unwrap collections (List<T>, T[], IEnumerable<T>, etc.)
        if (returnType is INamedTypeSymbol namedReturnType)
        {
            if (
                namedReturnType.OriginalDefinition.ToDisplayString()
                    == "System.Collections.Generic.List<T>"
                || namedReturnType.OriginalDefinition.ToDisplayString()
                    == "System.Collections.Generic.IEnumerable<T>"
            )
            {
                returnType = namedReturnType.TypeArguments[0];
            }
        }
        else if (returnType is IArrayTypeSymbol arrayType)
        {
            returnType = arrayType.ElementType;
        }

        // Skip if return type is void, primitive, or enum
        if (returnType.SpecialType != SpecialType.None || returnType.TypeKind == TypeKind.Enum)
        {
            return;
        }

        // Skip if from System or Dagger namespaces
        var namespaceName = returnType.ContainingNamespace?.ToDisplayString();
        if (
            namespaceName is not null
            && (namespaceName.StartsWith("System") || namespaceName.StartsWith("Dagger"))
        )
        {
            return;
        }

        // Check if the return type has [Object] or [Interface] attribute
        var hasObjectOrInterfaceAttribute = returnType
            .GetAttributes()
            .Any(attr =>
                IsAttributeType(attr, "Dagger", "ObjectAttribute")
                || IsAttributeType(attr, "Dagger", "Object")
                || IsAttributeType(attr, "Dagger", "InterfaceAttribute")
                || IsAttributeType(attr, "Dagger", "Interface")
            );

        if (!hasObjectOrInterfaceAttribute)
        {
            // Report diagnostic at the return type location
            var returnTypeLocation = methodDeclaration.ReturnType.GetLocation();

            var diagnostic = Diagnostic.Create(
                CustomReturnTypeMissingObjectAttribute,
                returnTypeLocation,
                returnType.Name
            );

            context.ReportDiagnostic(diagnostic);
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
}
