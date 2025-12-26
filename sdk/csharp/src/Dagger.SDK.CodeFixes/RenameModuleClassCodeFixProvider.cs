using System.Collections.Immutable;
using System.Composition;
using System.IO;
using System.Linq;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CodeActions;
using Microsoft.CodeAnalysis.CodeFixes;
using Microsoft.CodeAnalysis.CSharp.Syntax;
using Microsoft.CodeAnalysis.Rename;

namespace Dagger.SDK.Analyzers;

[
    ExportCodeFixProvider(LanguageNames.CSharp, Name = nameof(RenameModuleClassCodeFixProvider)),
    Shared
]
public class RenameModuleClassCodeFixProvider : CodeFixProvider
{
    public sealed override ImmutableArray<string> FixableDiagnosticIds =>
        [DiagnosticDescriptors.ModuleClassNameMismatch.Id];

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

        var classDeclaration = root.FindToken(diagnosticSpan.Start)
            .Parent?.AncestorsAndSelf()
            .OfType<ClassDeclarationSyntax>()
            .FirstOrDefault();

        if (classDeclaration == null)
            return;

        // Extract expected name from dagger.json
        // New message format: "At least one [Object] class must be named '{0}' to match dagger.json module name '{1}' and serve as the module root"
        var expectedName = GetExpectedNameFromDiagnostic(context.Document, classDeclaration);
        if (expectedName == null)
            return;

        context.RegisterCodeFix(
            CodeAction.Create(
                title: $"Rename class to '{expectedName}'",
                createChangedSolution: c =>
                    RenameClassAsync(context.Document, classDeclaration, expectedName, c),
                equivalenceKey: nameof(RenameModuleClassCodeFixProvider)
            ),
            diagnostic
        );
    }

    private async Task<Solution> RenameClassAsync(
        Document document,
        ClassDeclarationSyntax classDeclaration,
        string newName,
        CancellationToken cancellationToken
    )
    {
        var semanticModel = await document
            .GetSemanticModelAsync(cancellationToken)
            .ConfigureAwait(false);
        if (semanticModel == null)
            return document.Project.Solution;

        var classSymbol = semanticModel.GetDeclaredSymbol(classDeclaration, cancellationToken);
        if (classSymbol == null)
            return document.Project.Solution;

        // Use Roslyn's Renamer to rename the symbol and update all references
        var solution = document.Project.Solution;
        var newSolution = await Renamer
            .RenameSymbolAsync(
                solution,
                classSymbol,
                new SymbolRenameOptions(),
                newName,
                cancellationToken
            )
            .ConfigureAwait(false);

        return newSolution;
    }

    private string? GetExpectedNameFromDiagnostic(
        Document document,
        ClassDeclarationSyntax classDeclaration
    )
    {
        // Try to get dagger.json from the document's project additional documents
        var project = document.Project;
        var additionalFiles = project
            .AdditionalDocuments.Select(d => new AdditionalTextWrapper(d))
            .ToImmutableArray<AdditionalText>();

        var daggerConfig = DaggerJsonReader.FindDaggerJson(additionalFiles);
        if (daggerConfig == null)
            return null;

        return DaggerJsonReader.FormatName(daggerConfig.Name);
    }

    // Wrapper to convert TextDocument to AdditionalText for use with DaggerJsonReader
    private class AdditionalTextWrapper : AdditionalText
    {
        private readonly TextDocument _document;

        public AdditionalTextWrapper(TextDocument document)
        {
            _document = document;
        }

        public override string Path => _document.FilePath ?? string.Empty;

        public override Microsoft.CodeAnalysis.Text.SourceText? GetText(
            CancellationToken cancellationToken = default
        )
        {
            return _document.GetTextAsync(cancellationToken).Result;
        }
    }
}
