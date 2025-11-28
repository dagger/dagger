using System.Collections.Immutable;
using System.Composition;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CodeActions;
using Microsoft.CodeAnalysis.CodeFixes;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.CSharp.Syntax;

namespace Dagger.SDK.Analyzers;

[
    ExportCodeFixProvider(LanguageNames.CSharp, Name = nameof(FixInvalidCacheValueCodeFixProvider)),
    Shared
]
public class FixInvalidCacheValueCodeFixProvider : CodeFixProvider
{
    public sealed override ImmutableArray<string> FixableDiagnosticIds =>
        [DiagnosticDescriptors.InvalidFunctionCacheValue.Id];

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

        var attributeArgument = root.FindToken(diagnosticSpan.Start)
            .Parent?.AncestorsAndSelf()
            .OfType<AttributeArgumentSyntax>()
            .FirstOrDefault();

        if (attributeArgument == null)
            return;

        // Offer suggestions for common valid cache values
        var suggestions = new[]
        {
            ("never", "Use 'never' to disable caching"),
            ("session", "Use 'session' for per-session caching"),
            ("5m", "Use '5m' for 5 minute TTL"),
            ("1h", "Use '1h' for 1 hour TTL"),
            ("10s", "Use '10s' for 10 second TTL")
        };

        foreach (var (value, title) in suggestions)
        {
            context.RegisterCodeFix(
                CodeAction.Create(
                    title: title,
                    createChangedDocument: c =>
                        UpdateCacheValueAsync(context.Document, attributeArgument, value, c),
                    equivalenceKey: $"{nameof(FixInvalidCacheValueCodeFixProvider)}_{value}"
                ),
                diagnostic
            );
        }

        // Also offer to remove the Cache argument
        context.RegisterCodeFix(
            CodeAction.Create(
                title: "Remove Cache argument (use default 7-day TTL)",
                createChangedDocument: c =>
                    RemoveCacheArgumentAsync(context.Document, attributeArgument, c),
                equivalenceKey: $"{nameof(FixInvalidCacheValueCodeFixProvider)}_remove"
            ),
            diagnostic
        );
    }

    private async Task<Document> UpdateCacheValueAsync(
        Document document,
        AttributeArgumentSyntax attributeArgument,
        string newValue,
        CancellationToken cancellationToken
    )
    {
        var root = await document.GetSyntaxRootAsync(cancellationToken).ConfigureAwait(false);
        if (root == null)
            return document;

        // Create new literal expression with the corrected value
        var newLiteral = SyntaxFactory.LiteralExpression(
            SyntaxKind.StringLiteralExpression,
            SyntaxFactory.Literal(newValue)
        );

        // Create new attribute argument with the same name but new value
        var newArgument = attributeArgument.WithExpression(newLiteral);

        // Replace the old argument with the new one
        var newRoot = root.ReplaceNode(attributeArgument, newArgument);
        return document.WithSyntaxRoot(newRoot);
    }

    private async Task<Document> RemoveCacheArgumentAsync(
        Document document,
        AttributeArgumentSyntax attributeArgument,
        CancellationToken cancellationToken
    )
    {
        var root = await document.GetSyntaxRootAsync(cancellationToken).ConfigureAwait(false);
        if (root == null)
            return document;

        // Find the attribute argument list
        var argumentList = attributeArgument.Parent as AttributeArgumentListSyntax;
        if (argumentList == null)
            return document;

        // Remove the argument from the list
        var newArguments = argumentList.Arguments.Remove(attributeArgument);
        
        if (newArguments.Count == 0)
        {
            // If no arguments left, remove the entire argument list
            var attribute = argumentList.Parent as AttributeSyntax;
            if (attribute != null)
            {
                var newAttribute = attribute.WithArgumentList(null);
                var newRoot = root.ReplaceNode(attribute, newAttribute);
                return document.WithSyntaxRoot(newRoot);
            }
        }
        else
        {
            // Update the argument list
            var newArgumentList = argumentList.WithArguments(newArguments);
            var newRoot = root.ReplaceNode(argumentList, newArgumentList);
            return document.WithSyntaxRoot(newRoot);
        }

        return document;
    }
}
