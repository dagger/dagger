using System.Collections.Immutable;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.CSharp.Syntax;
using Microsoft.CodeAnalysis.Diagnostics;

namespace Dagger.SDK.Analyzers;

[DiagnosticAnalyzer(LanguageNames.CSharp)]
public class DaggerObjectAnalyzer : DiagnosticAnalyzer
{
    public override ImmutableArray<DiagnosticDescriptor> SupportedDiagnostics =>
        [
            DiagnosticDescriptors.PublicMethodInObjectMissingFunctionAttribute,
            DiagnosticDescriptors.FunctionMissingXmlDocumentation,
            DiagnosticDescriptors.ParameterMissingXmlDocumentation,
            DiagnosticDescriptors.ObjectClassMissingXmlDocumentation,
            DiagnosticDescriptors.FieldMissingXmlDocumentation,
            DiagnosticDescriptors.InvalidFunctionCacheValue,
        ];

    public override void Initialize(AnalysisContext context)
    {
        context.ConfigureGeneratedCodeAnalysis(GeneratedCodeAnalysisFlags.None);
        context.EnableConcurrentExecution();
        context.RegisterSyntaxNodeAction(AnalyzeClassDeclaration, SyntaxKind.ClassDeclaration);
    }

    private void AnalyzeClassDeclaration(SyntaxNodeAnalysisContext context)
    {
        var classDeclaration = (ClassDeclarationSyntax)context.Node;
        var classSymbol = context.SemanticModel.GetDeclaredSymbol(classDeclaration);

        if (classSymbol == null)
            return;

        // Check if class has [Object] attribute
        var hasObjectAttribute = classSymbol
            .GetAttributes()
            .Any(attr =>
                IsAttributeType(attr, "Dagger", "ObjectAttribute")
                || IsAttributeType(attr, "Dagger", "Object")
            );

        if (!hasObjectAttribute)
            return;

        // Check if class has XML documentation
        if (!HasXmlDocumentation(classDeclaration))
        {
            var diagnostic = Diagnostic.Create(
                DiagnosticDescriptors.ObjectClassMissingXmlDocumentation,
                classDeclaration.Identifier.GetLocation(),
                classSymbol.Name
            );
            context.ReportDiagnostic(diagnostic);
        }

        // Check all public methods
        foreach (var member in classDeclaration.Members)
        {
            if (member is MethodDeclarationSyntax methodDeclaration)
            {
                AnalyzeMethod(context, methodDeclaration, hasObjectAttribute);
            }
            else if (member is PropertyDeclarationSyntax propertyDeclaration)
            {
                AnalyzeProperty(context, propertyDeclaration);
            }
        }
    }

    private void AnalyzeMethod(
        SyntaxNodeAnalysisContext context,
        MethodDeclarationSyntax methodDeclaration,
        bool isInObjectClass
    )
    {
        // Only analyze public methods
        if (!methodDeclaration.Modifiers.Any(m => m.IsKind(SyntaxKind.PublicKeyword)))
            return;

        // Skip constructors
        if (
            methodDeclaration.Parent is ClassDeclarationSyntax parentClass
            && methodDeclaration.Identifier.Text == parentClass.Identifier.Text
        )
            return;

        var methodSymbol = context.SemanticModel.GetDeclaredSymbol(methodDeclaration);
        if (methodSymbol == null)
            return;

        // Check if method has [Function] attribute
        var hasFunctionAttribute = methodSymbol
            .GetAttributes()
            .Any(attr =>
                IsAttributeType(attr, "Dagger", "FunctionAttribute")
                || IsAttributeType(attr, "Dagger", "Function")
            );

        // Suggest [Function] attribute for public methods in [Object] classes
        if (isInObjectClass && !hasFunctionAttribute)
        {
            var diagnostic = Diagnostic.Create(
                DiagnosticDescriptors.PublicMethodInObjectMissingFunctionAttribute,
                methodDeclaration.Identifier.GetLocation(),
                methodSymbol.Name
            );
            context.ReportDiagnostic(diagnostic);
            return; // Don't check further if not a function
        }

        // If it has [Function], check for XML documentation
        if (hasFunctionAttribute)
        {
            // Validate Cache property value
            var functionAttribute = methodSymbol
                .GetAttributes()
                .FirstOrDefault(attr =>
                    IsAttributeType(attr, "Dagger", "FunctionAttribute")
                    || IsAttributeType(attr, "Dagger", "Function")
                );

            if (functionAttribute != null)
            {
                var cacheArgument = functionAttribute.NamedArguments.FirstOrDefault(arg =>
                    arg.Key == "Cache"
                );

                if (
                    cacheArgument.Value.Value is string cacheValue
                    && !string.IsNullOrWhiteSpace(cacheValue)
                )
                {
                    if (!IsValidCacheValue(cacheValue))
                    {
                        var diagnostic = Diagnostic.Create(
                            DiagnosticDescriptors.InvalidFunctionCacheValue,
                            GetCacheAttributeLocation(methodDeclaration, functionAttribute)
                                ?? methodDeclaration.Identifier.GetLocation(),
                            cacheValue
                        );
                        context.ReportDiagnostic(diagnostic);
                    }
                }
            }

            if (!HasXmlDocumentation(methodDeclaration))
            {
                var diagnostic = Diagnostic.Create(
                    DiagnosticDescriptors.FunctionMissingXmlDocumentation,
                    methodDeclaration.Identifier.GetLocation(),
                    methodSymbol.Name
                );
                context.ReportDiagnostic(diagnostic);
            }

            // Check parameters for XML documentation
            var xmlTrivia = methodDeclaration
                .GetLeadingTrivia()
                .Select(t => t.GetStructure())
                .OfType<DocumentationCommentTriviaSyntax>()
                .FirstOrDefault();

            var documentedParams = new HashSet<string>(
                xmlTrivia
                    ?.Content.OfType<XmlElementSyntax>()
                    .Where(e => e.StartTag?.Name?.ToString() == "param")
                    .Select(static e =>
                        e.StartTag?.Attributes.OfType<XmlNameAttributeSyntax>()
                            .FirstOrDefault()
                            ?.Identifier.ToString()
                    )
                    .Where(name => name != null)
                    .Select(name => name!) ?? []
            );

            foreach (var parameter in methodDeclaration.ParameterList.Parameters)
            {
                var paramName = parameter.Identifier.Text;
                if (!documentedParams.Contains(paramName))
                {
                    var diagnostic = Diagnostic.Create(
                        DiagnosticDescriptors.ParameterMissingXmlDocumentation,
                        parameter.Identifier.GetLocation(),
                        paramName
                    );
                    context.ReportDiagnostic(diagnostic);
                }
            }
        }
    }

    private void AnalyzeProperty(
        SyntaxNodeAnalysisContext context,
        PropertyDeclarationSyntax propertyDeclaration
    )
    {
        var propertySymbol = context.SemanticModel.GetDeclaredSymbol(propertyDeclaration);
        if (propertySymbol == null)
            return;

        // Check if property has [Field] attribute
        var hasFieldAttribute = propertySymbol
            .GetAttributes()
            .Any(attr =>
                IsAttributeType(attr, "Dagger", "FieldAttribute")
                || IsAttributeType(attr, "Dagger", "Field")
            );

        if (hasFieldAttribute && !HasXmlDocumentation(propertyDeclaration))
        {
            var diagnostic = Diagnostic.Create(
                DiagnosticDescriptors.FieldMissingXmlDocumentation,
                propertyDeclaration.Identifier.GetLocation(),
                propertySymbol.Name
            );
            context.ReportDiagnostic(diagnostic);
        }
    }

    private static bool HasXmlDocumentation(MemberDeclarationSyntax member)
    {
        return member
            .GetLeadingTrivia()
            .Any(t =>
                t.IsKind(SyntaxKind.SingleLineDocumentationCommentTrivia)
                || t.IsKind(SyntaxKind.MultiLineDocumentationCommentTrivia)
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

    private static bool IsValidCacheValue(string cacheValue)
    {
        // Valid values: "never", "session", or duration strings like "5m", "1h", "30s"
        if (
            cacheValue.Equals("never", StringComparison.OrdinalIgnoreCase)
            || cacheValue.Equals("session", StringComparison.OrdinalIgnoreCase)
        )
        {
            return true;
        }

        // Check if it's a valid duration string (e.g., "5m", "1h", "30s", "2h30m")
        return System.Text.RegularExpressions.Regex.IsMatch(
            cacheValue,
            @"^(\d+[smhd])+$",
            System.Text.RegularExpressions.RegexOptions.IgnoreCase
        );
    }

    private static Location? GetCacheAttributeLocation(
        MethodDeclarationSyntax methodDeclaration,
        AttributeData attributeData
    )
    {
        // Try to find the exact Cache argument location in the attribute syntax
        var attributeLists = methodDeclaration.AttributeLists;
        foreach (var attributeList in attributeLists)
        {
            foreach (var attribute in attributeList.Attributes)
            {
                if (attribute.ArgumentList?.Arguments != null)
                {
                    foreach (var argument in attribute.ArgumentList.Arguments)
                    {
                        if (argument.NameEquals?.Name.Identifier.Text == "Cache")
                        {
                            return argument.GetLocation();
                        }
                    }
                }
            }
        }

        return null;
    }
}
