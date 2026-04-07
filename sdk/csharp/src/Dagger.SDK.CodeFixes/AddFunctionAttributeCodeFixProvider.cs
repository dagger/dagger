using System.Collections.Immutable;
using System.Composition;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CodeActions;
using Microsoft.CodeAnalysis.CodeFixes;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.CSharp.Syntax;

namespace Dagger.SDK.Analyzers;

[
    ExportCodeFixProvider(LanguageNames.CSharp, Name = nameof(AddFunctionAttributeCodeFixProvider)),
    Shared
]
public class AddFunctionAttributeCodeFixProvider : CodeFixProvider
{
    public sealed override ImmutableArray<string> FixableDiagnosticIds =>
        [DiagnosticDescriptors.PublicMethodInObjectMissingFunctionAttribute.Id];

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

        var methodDeclaration = root.FindToken(diagnosticSpan.Start)
            .Parent?.AncestorsAndSelf()
            .OfType<MethodDeclarationSyntax>()
            .FirstOrDefault();

        if (methodDeclaration == null)
            return;

        context.RegisterCodeFix(
            CodeAction.Create(
                title: "Add [Function] attribute",
                createChangedDocument: c =>
                    AddFunctionAttributeAsync(context.Document, methodDeclaration, c),
                equivalenceKey: nameof(AddFunctionAttributeCodeFixProvider)
            ),
            diagnostic
        );
    }

    private async Task<Document> AddFunctionAttributeAsync(
        Document document,
        MethodDeclarationSyntax methodDeclaration,
        CancellationToken cancellationToken
    )
    {
        var root = await document.GetSyntaxRootAsync(cancellationToken).ConfigureAwait(false);
        if (root == null)
            return document;

        // Create [Function] attribute
        var functionAttribute = SyntaxFactory.Attribute(SyntaxFactory.IdentifierName("Function"));
        var attributeList = SyntaxFactory.AttributeList(
            SyntaxFactory.SingletonSeparatedList(functionAttribute)
        );

        // Add the attribute to the method
        var newMethod = methodDeclaration.AddAttributeLists(attributeList);

        // Replace the old method with the new one
        var newRoot = root.ReplaceNode(methodDeclaration, newMethod);
        return document.WithSyntaxRoot(newRoot);
    }
}
