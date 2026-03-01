// <copyright file="ConstructorAnalyzer.cs" company="Dagger">
// Copyright (c) Dagger. All rights reserved.
// </copyright>

using System.Collections.Immutable;
using System.Linq;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.CSharp.Syntax;
using Microsoft.CodeAnalysis.Diagnostics;

namespace Dagger.SDK.Analyzers;

/// <summary>
/// Analyzer that validates [Constructor] attribute usage on static factory methods.
/// </summary>
[DiagnosticAnalyzer(LanguageNames.CSharp)]
public class ConstructorAnalyzer : DiagnosticAnalyzer
{
    public override ImmutableArray<DiagnosticDescriptor> SupportedDiagnostics =>
        [
            DiagnosticDescriptors.ConstructorAttributeOnNonStaticMethod,
            DiagnosticDescriptors.ConstructorAttributeInvalidReturnType,
            DiagnosticDescriptors.MultipleConstructorAttributes,
        ];

    public override void Initialize(AnalysisContext context)
    {
        context.ConfigureGeneratedCodeAnalysis(GeneratedCodeAnalysisFlags.None);
        context.EnableConcurrentExecution();
        context.RegisterSyntaxNodeAction(AnalyzeMethod, SyntaxKind.MethodDeclaration);
        context.RegisterSymbolAction(AnalyzeClass, SymbolKind.NamedType);
    }

    private static void AnalyzeMethod(SyntaxNodeAnalysisContext context)
    {
        var methodDeclaration = (MethodDeclarationSyntax)context.Node;
        var methodSymbol = context.SemanticModel.GetDeclaredSymbol(methodDeclaration);

        if (methodSymbol == null)
        {
            return;
        }

        // Check if method has [Constructor] attribute
        var constructorAttribute = methodSymbol
            .GetAttributes()
            .FirstOrDefault(static a =>
                a.AttributeClass?.Name == "ConstructorAttribute"
                && a.AttributeClass?.ContainingNamespace?.ToDisplayString() == "Dagger"
            );

        if (constructorAttribute == null)
        {
            return;
        }

        // DAGGER019: Must be static
        if (!methodSymbol.IsStatic)
        {
            var diagnostic = Diagnostic.Create(
                DiagnosticDescriptors.ConstructorAttributeOnNonStaticMethod,
                methodDeclaration.Identifier.GetLocation(),
                methodSymbol.Name
            );
            context.ReportDiagnostic(diagnostic);
            return;
        }

        // DAGGER020: Must return containing class type (or Task/ValueTask of it)
        var containingType = methodSymbol.ContainingType;
        var returnType = methodSymbol.ReturnType;

        bool validReturnType = false;

        // Direct return: public static MyModule Create()
        if (SymbolEqualityComparer.Default.Equals(returnType, containingType))
        {
            validReturnType = true;
        }
        // Task<MyModule> or ValueTask<MyModule>
        else if (
            returnType is INamedTypeSymbol namedReturnType
            && namedReturnType.IsGenericType
            && namedReturnType.TypeArguments.Length == 1
        )
        {
            var originalDefinition = namedReturnType.OriginalDefinition;
            var containingNamespace = originalDefinition.ContainingNamespace?.ToDisplayString();
            var typeName = originalDefinition.Name;

            // Check if it's Task<T> or ValueTask<T> from System.Threading.Tasks
            if (
                containingNamespace == "System.Threading.Tasks"
                && (typeName == "Task" || typeName == "ValueTask")
            )
            {
                var taskInnerType = namedReturnType.TypeArguments[0];
                if (SymbolEqualityComparer.Default.Equals(taskInnerType, containingType))
                {
                    validReturnType = true;
                }
            }
        }

        if (!validReturnType)
        {
            var diagnostic = Diagnostic.Create(
                DiagnosticDescriptors.ConstructorAttributeInvalidReturnType,
                methodDeclaration.ReturnType.GetLocation(),
                methodSymbol.Name,
                returnType.ToDisplayString(),
                containingType.Name
            );
            context.ReportDiagnostic(diagnostic);
        }
    }

    private static void AnalyzeClass(SymbolAnalysisContext context)
    {
        var classSymbol = (INamedTypeSymbol)context.Symbol;

        // Count methods with [Constructor] attribute
        var constructorMethods = classSymbol
            .GetMembers()
            .OfType<IMethodSymbol>()
            .Where(m =>
                m.GetAttributes()
                    .Any(a =>
                        a.AttributeClass?.Name == "ConstructorAttribute"
                        && a.AttributeClass?.ContainingNamespace?.ToDisplayString() == "Dagger"
                    )
            )
            .ToList();

        // DAGGER021: Only one [Constructor] allowed
        if (constructorMethods.Count > 1)
        {
            foreach (var method in constructorMethods)
            {
                var methodSyntax = method.DeclaringSyntaxReferences.FirstOrDefault()?.GetSyntax();
                if (methodSyntax is MethodDeclarationSyntax methodDeclaration)
                {
                    var diagnostic = Diagnostic.Create(
                        DiagnosticDescriptors.MultipleConstructorAttributes,
                        methodDeclaration.Identifier.GetLocation(),
                        classSymbol.Name,
                        constructorMethods.Count
                    );
                    context.ReportDiagnostic(diagnostic);
                }
            }
        }
    }
}
