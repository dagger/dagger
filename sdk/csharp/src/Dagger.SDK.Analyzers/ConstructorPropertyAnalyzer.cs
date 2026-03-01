using System.Collections.Immutable;
using System.Linq;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.CSharp.Syntax;
using Microsoft.CodeAnalysis.Diagnostics;

namespace Dagger.SDK.Analyzers;

/// <summary>
/// Analyzer that ensures constructor parameters in Dagger objects map to public properties.
/// Dagger serializes objects as JSON using public properties, so private fields won't be preserved
/// across function calls.
/// </summary>
[DiagnosticAnalyzer(LanguageNames.CSharp)]
public class ConstructorPropertyAnalyzer : DiagnosticAnalyzer
{
    public override ImmutableArray<DiagnosticDescriptor> SupportedDiagnostics =>
        ImmutableArray.Create(DiagnosticDescriptors.ConstructorParameterShouldMapToPublicProperty);

    public override void Initialize(AnalysisContext context)
    {
        context.ConfigureGeneratedCodeAnalysis(GeneratedCodeAnalysisFlags.None);
        context.EnableConcurrentExecution();
        context.RegisterSyntaxNodeAction(AnalyzeConstructor, SyntaxKind.ConstructorDeclaration);
    }

    private static void AnalyzeConstructor(SyntaxNodeAnalysisContext context)
    {
        var constructorDeclaration = (ConstructorDeclarationSyntax)context.Node;

        // Get the containing class
        if (constructorDeclaration.Parent is not ClassDeclarationSyntax classDeclaration)
        {
            return;
        }

        // Check if class has [Object] attribute
        var classSymbol = context.SemanticModel.GetDeclaredSymbol(classDeclaration);
        if (classSymbol == null)
        {
            return;
        }

        var hasObjectAttribute = classSymbol
            .GetAttributes()
            .Any(attr =>
                attr.AttributeClass?.Name == "ObjectAttribute"
                && attr.AttributeClass.ContainingNamespace.ToDisplayString() == "Dagger"
            );

        if (!hasObjectAttribute)
        {
            return;
        }

        // Get all public properties in the class
        var publicProperties = classSymbol
            .GetMembers()
            .OfType<IPropertySymbol>()
            .Where(p => p.DeclaredAccessibility == Accessibility.Public && p.SetMethod != null)
            .ToImmutableArray();

        // Check each constructor parameter
        foreach (var parameter in constructorDeclaration.ParameterList.Parameters)
        {
            var parameterSymbol = context.SemanticModel.GetDeclaredSymbol(parameter);
            if (parameterSymbol == null)
            {
                continue;
            }

            var parameterName = parameterSymbol.Name;

            // Look for a matching public property (case-insensitive)
            var matchingProperty = publicProperties.FirstOrDefault(prop =>
                string.Equals(prop.Name, parameterName, System.StringComparison.OrdinalIgnoreCase)
            );

            if (matchingProperty == null)
            {
                // Check if constructor body assigns to a private field
                var assignsToPrivateField = CheckAssignsToPrivateField(
                    constructorDeclaration,
                    parameterName,
                    context.SemanticModel
                );

                if (assignsToPrivateField)
                {
                    var diagnostic = Diagnostic.Create(
                        DiagnosticDescriptors.ConstructorParameterShouldMapToPublicProperty,
                        parameter.Identifier.GetLocation(),
                        parameterName
                    );
                    context.ReportDiagnostic(diagnostic);
                }
            }
        }
    }

    private static bool CheckAssignsToPrivateField(
        ConstructorDeclarationSyntax constructor,
        string parameterName,
        SemanticModel semanticModel
    )
    {
        if (constructor.Body == null && constructor.ExpressionBody == null)
        {
            return false;
        }

        var assignments = constructor
            .DescendantNodes()
            .OfType<AssignmentExpressionSyntax>()
            .Where(assignment => assignment.IsKind(SyntaxKind.SimpleAssignmentExpression));

        foreach (var assignment in assignments)
        {
            // Check if right side is the parameter
            if (assignment.Right is IdentifierNameSyntax rightIdentifier)
            {
                var rightSymbol = semanticModel.GetSymbolInfo(rightIdentifier).Symbol;
                if (
                    rightSymbol is IParameterSymbol paramSymbol
                    && paramSymbol.Name == parameterName
                )
                {
                    // Check if left side is a field
                    var leftSymbol = semanticModel.GetSymbolInfo(assignment.Left).Symbol;
                    if (
                        leftSymbol is IFieldSymbol fieldSymbol
                        && fieldSymbol.DeclaredAccessibility == Accessibility.Private
                    )
                    {
                        return true;
                    }
                }
            }
        }

        return false;
    }
}
