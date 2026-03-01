using System.Collections.Immutable;
using System.Composition;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CodeActions;
using Microsoft.CodeAnalysis.CodeFixes;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.CSharp.Syntax;

namespace Dagger.SDK.Analyzers;

[
    ExportCodeFixProvider(LanguageNames.CSharp, Name = nameof(AddPrivateSetterCodeFixProvider)),
    Shared
]
public class AddPrivateSetterCodeFixProvider : CodeFixProvider
{
    public sealed override ImmutableArray<string> FixableDiagnosticIds =>
        [DiagnosticDescriptors.FieldPropertyMustHaveSetter.Id];

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
        var token = root.FindToken(diagnosticSpan.Start);

        var property = token
            .Parent?.AncestorsAndSelf()
            .OfType<PropertyDeclarationSyntax>()
            .FirstOrDefault();
        if (property == null)
            return;

        context.RegisterCodeFix(
            CodeAction.Create(
                title: "Add private setter",
                createChangedDocument: c => AddPrivateSetterAsync(context.Document, property, c),
                equivalenceKey: nameof(AddPrivateSetterCodeFixProvider)
            ),
            diagnostic
        );
    }

    private async Task<Document> AddPrivateSetterAsync(
        Document document,
        PropertyDeclarationSyntax property,
        CancellationToken cancellationToken
    )
    {
        var root = await document.GetSyntaxRootAsync(cancellationToken).ConfigureAwait(false);
        if (root == null)
            return document;

        // Check if property has a getter
        var getter = property.AccessorList?.Accessors.FirstOrDefault(a =>
            a.IsKind(SyntaxKind.GetAccessorDeclaration)
        );
        if (getter == null)
            return document;

        // Create a private setter
        var setter = SyntaxFactory
            .AccessorDeclaration(SyntaxKind.SetAccessorDeclaration)
            .WithModifiers(SyntaxFactory.TokenList(SyntaxFactory.Token(SyntaxKind.PrivateKeyword)))
            .WithSemicolonToken(SyntaxFactory.Token(SyntaxKind.SemicolonToken));

        // Add setter to accessor list
        var newAccessorList = property
            .AccessorList!.AddAccessors(setter)
            .WithTrailingTrivia(property.AccessorList.GetTrailingTrivia());

        var newProperty = property.WithAccessorList(newAccessorList);

        var newRoot = root.ReplaceNode(property, newProperty);
        return document.WithSyntaxRoot(newRoot);
    }
}
