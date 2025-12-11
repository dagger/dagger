using System.Collections.Immutable;
using System.Composition;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CodeActions;
using Microsoft.CodeAnalysis.CodeFixes;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.CSharp.Syntax;

namespace Dagger.SDK.Analyzers;

[
    ExportCodeFixProvider(LanguageNames.CSharp, Name = nameof(AddXmlDocumentationCodeFixProvider)),
    Shared
]
public class AddXmlDocumentationCodeFixProvider : CodeFixProvider
{
    public sealed override ImmutableArray<string> FixableDiagnosticIds =>
        [
            DiagnosticDescriptors.FunctionMissingXmlDocumentation.Id,
            DiagnosticDescriptors.ObjectClassMissingXmlDocumentation.Id,
            DiagnosticDescriptors.FieldMissingXmlDocumentation.Id,
        ];

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

        // Find the member (method, class, or property)
        var member = token
            .Parent?.AncestorsAndSelf()
            .OfType<MemberDeclarationSyntax>()
            .FirstOrDefault();
        if (member == null)
            return;

        var title = member switch
        {
            ClassDeclarationSyntax => "Add XML documentation to class",
            MethodDeclarationSyntax => "Add XML documentation to function",
            PropertyDeclarationSyntax => "Add XML documentation to field",
            _ => "Add XML documentation",
        };

        context.RegisterCodeFix(
            CodeAction.Create(
                title: title,
                createChangedDocument: c => AddXmlDocumentationAsync(context.Document, member, c),
                equivalenceKey: nameof(AddXmlDocumentationCodeFixProvider)
            ),
            diagnostic
        );
    }

    private async Task<Document> AddXmlDocumentationAsync(
        Document document,
        MemberDeclarationSyntax member,
        CancellationToken cancellationToken
    )
    {
        var root = await document.GetSyntaxRootAsync(cancellationToken).ConfigureAwait(false);
        if (root == null)
            return document;

        var summaryText = member switch
        {
            ClassDeclarationSyntax cls => $"TODO: Describe what {cls.Identifier.Text} does",
            MethodDeclarationSyntax method => $"TODO: Describe what {method.Identifier.Text} does",
            PropertyDeclarationSyntax prop => $"TODO: Describe the {prop.Identifier.Text} field",
            _ => "TODO: Add description",
        };

        // Create XML documentation comment
        var summaryElement = SyntaxFactory.XmlSummaryElement(
            SyntaxFactory.XmlText($" {summaryText} ")
        );

        var xmlComment = SyntaxFactory.DocumentationCommentTrivia(
            SyntaxKind.SingleLineDocumentationCommentTrivia,
            SyntaxFactory.List(new XmlNodeSyntax[] { summaryElement })
        );

        // Add parameter documentation for methods
        if (
            member is MethodDeclarationSyntax methodDecl
            && methodDecl.ParameterList.Parameters.Any()
        )
        {
            var nodes = new System.Collections.Generic.List<XmlNodeSyntax> { summaryElement };

            foreach (var param in methodDecl.ParameterList.Parameters)
            {
                var paramElement = SyntaxFactory.XmlParamElement(
                    param.Identifier.Text,
                    SyntaxFactory.XmlText($" TODO: Describe {param.Identifier.Text} ")
                );
                nodes.Add(paramElement);
            }

            xmlComment = SyntaxFactory.DocumentationCommentTrivia(
                SyntaxKind.SingleLineDocumentationCommentTrivia,
                SyntaxFactory.List(nodes)
            );
        }

        // Create the trivia with proper formatting
        var trivia = SyntaxFactory.Trivia(xmlComment);
        var leadingTrivia = member.GetLeadingTrivia().Insert(0, trivia);

        var newMember = member.WithLeadingTrivia(leadingTrivia);
        var newRoot = root.ReplaceNode(member, newMember);

        return document.WithSyntaxRoot(newRoot);
    }
}
