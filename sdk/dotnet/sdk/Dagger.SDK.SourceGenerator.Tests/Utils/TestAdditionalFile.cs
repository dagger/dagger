using System.Threading;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.Text;

namespace Dagger.SDK.SourceGenerator.Tests.Utils;

public class TestAdditionalFile(string path, string text) : AdditionalText
{
    private readonly SourceText _text = SourceText.From(text);

    public override SourceText GetText(CancellationToken cancellationToken = new()) => _text;

    public override string Path { get; } = path;
}
