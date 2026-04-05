using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.Text;

namespace Dagger.SDK.Analyzers.Tests.Helpers;

/// <summary>
/// Test implementation of AdditionalText for use in analyzer tests.
/// Allows mocking additional files like dagger.json.
/// </summary>
public class TestAdditionalFile : AdditionalText
{
    private readonly SourceText _text;

    public TestAdditionalFile(string path, string text)
    {
        Path = path;
        _text = SourceText.From(text);
    }

    public override string Path { get; }

    public override SourceText GetText(CancellationToken cancellationToken = default) => _text;
}
