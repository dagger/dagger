using System.Collections.Immutable;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.CSharp.Syntax;
using Microsoft.CodeAnalysis.Diagnostics;

namespace Dagger.SDK.Analyzers;

[DiagnosticAnalyzer(LanguageNames.CSharp)]
public class EnumValueAnalyzer : DiagnosticAnalyzer
{
    public override ImmutableArray<DiagnosticDescriptor> SupportedDiagnostics =>
        [DiagnosticDescriptors.EnumMemberMissingEnumValueAttribute];

    public override void Initialize(AnalysisContext context)
    {
        context.ConfigureGeneratedCodeAnalysis(GeneratedCodeAnalysisFlags.None);
        context.EnableConcurrentExecution();
        context.RegisterSyntaxNodeAction(AnalyzeEnumDeclaration, SyntaxKind.EnumDeclaration);
    }

    private void AnalyzeEnumDeclaration(SyntaxNodeAnalysisContext context)
    {
        var enumDeclaration = (EnumDeclarationSyntax)context.Node;
        var enumSymbol = context.SemanticModel.GetDeclaredSymbol(enumDeclaration);

        if (enumSymbol == null)
            return;

        // Check if enum has [Enum] attribute
        var hasEnumAttribute = enumSymbol
            .GetAttributes()
            .Any(attr =>
                IsAttributeType(attr, "Dagger", "EnumAttribute")
                || IsAttributeType(attr, "Dagger", "Enum")
            );

        if (!hasEnumAttribute)
            return;

        // Check all enum members for [EnumValue] attribute
        foreach (var member in enumDeclaration.Members)
        {
            var memberSymbol = context.SemanticModel.GetDeclaredSymbol(member);
            if (memberSymbol == null)
                continue;

            // Check if member has [EnumValue] attribute
            var hasEnumValueAttribute = memberSymbol
                .GetAttributes()
                .Any(attr =>
                    IsAttributeType(attr, "Dagger", "EnumValueAttribute")
                    || IsAttributeType(attr, "Dagger", "EnumValue")
                );

            if (!hasEnumValueAttribute)
            {
                var diagnostic = Diagnostic.Create(
                    DiagnosticDescriptors.EnumMemberMissingEnumValueAttribute,
                    member.Identifier.GetLocation(),
                    memberSymbol.Name,
                    enumSymbol.Name
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
}
