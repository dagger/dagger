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
        Name = nameof(AddDefaultPathAttributeCodeFixProvider)
    ),
    Shared
]
public class AddDefaultPathAttributeCodeFixProvider : CodeFixProvider
{
    public sealed override ImmutableArray<string> FixableDiagnosticIds =>
        [DiagnosticDescriptors.DirectoryParameterShouldHaveDefaultPath.Id];

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

        var parameter = root.FindToken(diagnosticSpan.Start)
            .Parent?.AncestorsAndSelf()
            .OfType<ParameterSyntax>()
            .FirstOrDefault();

        if (parameter == null)
            return;

        context.RegisterCodeFix(
            CodeAction.Create(
                title: "Add [DefaultPath(\".\")] attribute",
                createChangedDocument: c =>
                    AddDefaultPathAttributeAsync(context.Document, parameter, c),
                equivalenceKey: nameof(AddDefaultPathAttributeCodeFixProvider)
            ),
            diagnostic
        );
    }

    private async Task<Document> AddDefaultPathAttributeAsync(
        Document document,
        ParameterSyntax parameter,
        CancellationToken cancellationToken
    )
    {
        var root = await document.GetSyntaxRootAsync(cancellationToken).ConfigureAwait(false);
        if (root == null)
            return document;

        // Create [DefaultPath(".")] attribute
        var argument = SyntaxFactory.AttributeArgument(
            SyntaxFactory.LiteralExpression(
                SyntaxKind.StringLiteralExpression,
                SyntaxFactory.Literal(".")
            )
        );

        var attribute = SyntaxFactory.Attribute(
            SyntaxFactory.IdentifierName("DefaultPath"),
            SyntaxFactory.AttributeArgumentList(SyntaxFactory.SingletonSeparatedList(argument))
        );

        var attributeList = SyntaxFactory.AttributeList(
            SyntaxFactory.SingletonSeparatedList(attribute)
        );

        // Add the attribute to the parameter
        var newParameter = parameter.AddAttributeLists(attributeList);

        // Replace the old parameter with the new one
        var newRoot = root.ReplaceNode(parameter, newParameter);
        return document.WithSyntaxRoot(newRoot);
    }
}
