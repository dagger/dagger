using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CodeFixes;
using Microsoft.CodeAnalysis.CSharp.Testing;
using Microsoft.CodeAnalysis.Diagnostics;
using Microsoft.CodeAnalysis.Testing;

namespace Dagger.SDK.Analyzers.Tests.Helpers;

/// <summary>
/// Provides verification helpers for Roslyn code fix providers using MSTest.
/// </summary>
/// <typeparam name="TAnalyzer">The type of analyzer.</typeparam>
/// <typeparam name="TCodeFix">The type of code fix provider.</typeparam>
public static class CSharpCodeFixVerifier<TAnalyzer, TCodeFix>
    where TAnalyzer : DiagnosticAnalyzer, new()
    where TCodeFix : CodeFixProvider, new()
{
    /// <inheritdoc cref="CodeFixVerifier{TAnalyzer, TCodeFix, TTest, TVerifier}.Diagnostic()"/>
    public static DiagnosticResult Diagnostic()
    {
        return CSharpCodeFixVerifier<TAnalyzer, TCodeFix, DefaultVerifier>.Diagnostic();
    }

    /// <inheritdoc cref="CodeFixVerifier{TAnalyzer, TCodeFix, TTest, TVerifier}.Diagnostic(string)"/>
    public static DiagnosticResult Diagnostic(string diagnosticId)
    {
        return CSharpCodeFixVerifier<TAnalyzer, TCodeFix, DefaultVerifier>.Diagnostic(diagnosticId);
    }

    /// <inheritdoc cref="CodeFixVerifier{TAnalyzer, TCodeFix, TTest, TVerifier}.Diagnostic(DiagnosticDescriptor)"/>
    public static DiagnosticResult Diagnostic(DiagnosticDescriptor descriptor)
    {
        return CSharpCodeFixVerifier<TAnalyzer, TCodeFix, DefaultVerifier>.Diagnostic(descriptor);
    }

    /// <inheritdoc cref="CodeFixVerifier{TAnalyzer, TCodeFix, TTest, TVerifier}.VerifyCodeFixAsync(string, string)"/>
    public static async Task VerifyCodeFixAsync(
        string source,
        string fixedSource,
        int? codeFixIndex = null,
        Action<CSharpCodeFixTest<TAnalyzer, TCodeFix, DefaultVerifier>>? configureTest = null
    )
    {
        await VerifyCodeFixAsync(
            source,
            DiagnosticResult.EmptyDiagnosticResults,
            DiagnosticResult.EmptyDiagnosticResults,
            fixedSource,
            codeFixIndex,
            configureTest: configureTest
        );
    }

    /// <inheritdoc cref="CodeFixVerifier{TAnalyzer, TCodeFix, TTest, TVerifier}.VerifyCodeFixAsync(string, DiagnosticResult, string)"/>
    public static async Task VerifyCodeFixAsync(
        string source,
        DiagnosticResult expected,
        string fixedSource,
        int? codeFixIndex = null,
        Action<CSharpCodeFixTest<TAnalyzer, TCodeFix, DefaultVerifier>>? configureTest = null
    )
    {
        await VerifyCodeFixAsync(
            source,
            [expected],
            DiagnosticResult.EmptyDiagnosticResults,
            fixedSource,
            codeFixIndex,
            configureTest: configureTest
        );
    }

    /// <inheritdoc cref="CodeFixVerifier{TAnalyzer, TCodeFix, TTest, TVerifier}.VerifyCodeFixAsync(string, DiagnosticResult[], string)"/>
    public static async Task VerifyCodeFixAsync(
        string source,
        DiagnosticResult[] expected,
        string fixedSource,
        int? codeFixIndex = null,
        Action<CSharpCodeFixTest<TAnalyzer, TCodeFix, DefaultVerifier>>? configureTest = null
    )
    {
        await VerifyCodeFixAsync(
            source,
            expected,
            DiagnosticResult.EmptyDiagnosticResults,
            fixedSource,
            codeFixIndex,
            configureTest: configureTest
        );
    }

    /// <summary>
    /// Verifies code fix and allows specifying diagnostics that should remain after the fix is applied.
    /// </summary>
    public static async Task VerifyCodeFixAsync(
        string source,
        DiagnosticResult[] expected,
        DiagnosticResult[] fixedExpected,
        string fixedSource,
        int? codeFixIndex = null,
        Action<CSharpCodeFixTest<TAnalyzer, TCodeFix, DefaultVerifier>>? configureTest = null
    )
    {
        var test = new Test { TestCode = source, FixedCode = fixedSource };

        test.ExpectedDiagnostics.AddRange(expected);
        test.FixedState.ExpectedDiagnostics.AddRange(fixedExpected);
        if (codeFixIndex.HasValue)
        {
            test.CodeActionIndex = codeFixIndex.Value;
        }

        configureTest?.Invoke(test);

        await test.RunAsync(CancellationToken.None);
    }

    /// <summary>
    /// Verifies code fix with additional files (e.g., dagger.json).
    /// </summary>
    public static async Task VerifyCodeFixAsync(
        string source,
        IEnumerable<(string path, string content)> additionalFiles,
        DiagnosticResult[] expected,
        string fixedSource,
        int? codeFixIndex = null,
        string? assemblyName = null,
        Action<CSharpCodeFixTest<TAnalyzer, TCodeFix, DefaultVerifier>>? configureTest = null
    )
    {
        await VerifyCodeFixAsync(
            source,
            additionalFiles,
            expected,
            DiagnosticResult.EmptyDiagnosticResults,
            fixedSource,
            codeFixIndex,
            assemblyName,
            configureTest
        );
    }

    /// <summary>
    /// Verifies code fix with additional files and allows specifying diagnostics that should remain after the fix.
    /// </summary>
    public static async Task VerifyCodeFixAsync(
        string source,
        IEnumerable<(string path, string content)> additionalFiles,
        DiagnosticResult[] expected,
        DiagnosticResult[] fixedExpected,
        string fixedSource,
        int? codeFixIndex = null,
        string? assemblyName = null,
        Action<CSharpCodeFixTest<TAnalyzer, TCodeFix, DefaultVerifier>>? configureTest = null
    )
    {
        var test = new Test { TestCode = source, FixedCode = fixedSource };

        foreach (var (path, content) in additionalFiles)
        {
            if (
                path.EndsWith(".editorconfig", StringComparison.OrdinalIgnoreCase)
                || path.EndsWith(".globalconfig", StringComparison.OrdinalIgnoreCase)
            )
            {
                var baseDirectory = AppContext.BaseDirectory;
                var resolvedPath = Path.IsPathRooted(path)
                    ? path
                    : Path.Combine(baseDirectory, path);

                test.TestState.AnalyzerConfigFiles.Add((resolvedPath, content));
                test.FixedState.AnalyzerConfigFiles.Add((resolvedPath, content));
            }
            else
            {
                test.TestState.AdditionalFiles.Add((path, content));
                test.FixedState.AdditionalFiles.Add((path, content));
            }
        }

        if (!string.IsNullOrEmpty(assemblyName))
        {
            test.SolutionTransforms.Add(
                (solution, projectId) =>
                {
                    var project = solution.GetProject(projectId);
                    if (project is not null)
                    {
                        solution = solution.WithProjectAssemblyName(projectId, assemblyName);
                    }

                    return solution;
                }
            );
        }

        test.ExpectedDiagnostics.AddRange(expected);
        test.FixedState.ExpectedDiagnostics.AddRange(fixedExpected);
        if (codeFixIndex.HasValue)
        {
            test.CodeActionIndex = codeFixIndex.Value;
        }

        configureTest?.Invoke(test);

        await test.RunAsync(CancellationToken.None);
    }

    /// <summary>
    /// Custom test class for CSharp code fix verification.
    /// </summary>
    private class Test : CSharpCodeFixTest<TAnalyzer, TCodeFix, DefaultVerifier>
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
