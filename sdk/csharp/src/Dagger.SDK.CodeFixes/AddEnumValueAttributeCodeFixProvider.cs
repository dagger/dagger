using System.Collections.Immutable;
using System.Composition;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CodeActions;
using Microsoft.CodeAnalysis.CodeFixes;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.CSharp.Syntax;

namespace Dagger.SDK.Analyzers;

[
    ExportCodeFixProvider(
        LanguageNames.CSharp,
        Name = nameof(AddEnumValueAttributeCodeFixProvider)
    ),
    Shared
]
public class AddEnumValueAttributeCodeFixProvider : CodeFixProvider
{
    public sealed override ImmutableArray<string> FixableDiagnosticIds =>
        [DiagnosticDescriptors.EnumMemberMissingEnumValueAttribute.Id];

    public sealed override FixAllProvider GetFixAllProvider() =>
        WellKnownFixAllProviders.BatchFixer;

    public sealed override async Task RegisterCodeFixesAsync(CodeFixContext context)
    {
        var root = await context
            .Document.GetSyntaxRootAsync(context.CancellationToken)
            .ConfigureAwait(false);
        if (root == null)
            return;

        var diagnostic = context.Diagnostics.First();
        var diagnosticSpan = diagnostic.Location.SourceSpan;

        var enumMemberDeclaration = root.FindToken(diagnosticSpan.Start)
            .Parent?.AncestorsAndSelf()
            .OfType<EnumMemberDeclarationSyntax>()
            .FirstOrDefault();

        if (enumMemberDeclaration == null)
            return;

        context.RegisterCodeFix(
            CodeAction.Create(
                title: "Add [EnumValue] attribute",
                createChangedDocument: c =>
                    AddEnumValueAttributeAsync(context.Document, enumMemberDeclaration, c),
                equivalenceKey: nameof(AddEnumValueAttributeCodeFixProvider)
            ),
            diagnostic
        );
    }

    private async Task<Document> AddEnumValueAttributeAsync(
        Document document,
        EnumMemberDeclarationSyntax enumMemberDeclaration,
        CancellationToken cancellationToken
    )
    {
        var root = await document.GetSyntaxRootAsync(cancellationToken).ConfigureAwait(false);
        if (root == null)
            return document;

        // Create [EnumValue] attribute
        var enumValueAttribute = SyntaxFactory.Attribute(SyntaxFactory.IdentifierName("EnumValue"));
        var attributeList = SyntaxFactory.AttributeList(
            SyntaxFactory.SingletonSeparatedList(enumValueAttribute)
        );

        // Add the attribute to the enum member
        var newEnumMember = enumMemberDeclaration.AddAttributeLists(attributeList);

        // Replace the old enum member with the new one
        var newRoot = root.ReplaceNode(enumMemberDeclaration, newEnumMember);
        return document.WithSyntaxRoot(newRoot);
    }
}
