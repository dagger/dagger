using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp.Testing;
using Microsoft.CodeAnalysis.Diagnostics;
using Microsoft.CodeAnalysis.Testing;

namespace Dagger.SDK.Analyzers.Tests.Helpers;

/// <summary>
/// Provides verification helpers for Roslyn analyzers using MSTest.
/// </summary>
/// <typeparam name="TAnalyzer">The type of analyzer to verify.</typeparam>
public static class CSharpAnalyzerVerifier<TAnalyzer>
    where TAnalyzer : DiagnosticAnalyzer, new()
{
    /// <inheritdoc cref="AnalyzerVerifier{TAnalyzer, TTest, TVerifier}.Diagnostic()"/>
    public static DiagnosticResult Diagnostic()
    {
        return CSharpAnalyzerVerifier<TAnalyzer, DefaultVerifier>.Diagnostic();
    }

    /// <inheritdoc cref="AnalyzerVerifier{TAnalyzer, TTest, TVerifier}.Diagnostic(string)"/>
    public static DiagnosticResult Diagnostic(string diagnosticId)
    {
        return CSharpAnalyzerVerifier<TAnalyzer, DefaultVerifier>.Diagnostic(diagnosticId);
    }

    /// <inheritdoc cref="AnalyzerVerifier{TAnalyzer, TTest, TVerifier}.Diagnostic(DiagnosticDescriptor)"/>
    public static DiagnosticResult Diagnostic(DiagnosticDescriptor descriptor)
    {
        return CSharpAnalyzerVerifier<TAnalyzer, DefaultVerifier>.Diagnostic(descriptor);
    }

    /// <inheritdoc cref="AnalyzerVerifier{TAnalyzer, TTest, TVerifier}.VerifyAnalyzerAsync(string, DiagnosticResult[])"/>
    public static async Task VerifyAnalyzerAsync(string source, params DiagnosticResult[] expected)
    {
        var test = new Test { TestCode = source };

        test.ExpectedDiagnostics.AddRange(expected);
        await test.RunAsync(CancellationToken.None);
    }

    /// <summary>
    /// Verifies analyzer with additional files (e.g., dagger.json).
    /// </summary>
    public static async Task VerifyAnalyzerAsync(
        string source,
        IEnumerable<(string path, string content)> additionalFiles,
        params DiagnosticResult[] expected
    )
    {
        var test = new Test { TestCode = source };

        foreach (var (path, content) in additionalFiles)
        {
            test.TestState.AdditionalFiles.Add((path, content));
        }

        test.ExpectedDiagnostics.AddRange(expected);
        await test.RunAsync(CancellationToken.None);
    }

    /// <summary>
    /// Custom test class for CSharp analyzer verification.
    /// </summary>
    private class Test : CSharpAnalyzerTest<TAnalyzer, DefaultVerifier>
    {
        public Test()
        {
            // Add Dagger.SDK assembly reference so attributes and types can be resolved
            TestState.AdditionalReferences.Add(typeof(Dagger.ObjectAttribute).Assembly);

            // Ignore compiler errors - we only care about analyzer diagnostics
            CompilerDiagnostics = CompilerDiagnostics.None;
        }
    }
}
