using static Dagger.SDK.Analyzers.DiagnosticDescriptors;
using VerifyCS = Dagger.SDK.Analyzers.Tests.Helpers.CSharpAnalyzerVerifier<Dagger.SDK.Analyzers.DaggerDirectoryAnalyzer>;

namespace Dagger.SDK.Analyzers.Tests.Analyzers;

/// <summary>
/// Tests for DaggerDirectoryAnalyzer (DAGGER004-005).
/// </summary>
[TestClass]
public class DaggerDirectoryAnalyzerTests
{
    #region DAGGER004: Directory parameter should consider [DefaultPath] attribute

    [TestMethod]
    public async Task DirectoryParameter_WithoutDefaultPath_ReportsDiagnostics()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string Build(Directory {|#0:source|}) => "built";
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(DirectoryParameterShouldHaveDefaultPath)
                .WithLocation(0)
                .WithArguments("source"),
            VerifyCS
                .Diagnostic(DirectoryParameterShouldHaveIgnore)
                .WithLocation(0)
                .WithArguments("source"),
        };

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task DirectoryParameter_WithDefaultPath_StillRequiresIgnore()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string Build([DefaultPath(".")] Directory source) => "built";
            }
            """;

        var expected = VerifyCS
            .Diagnostic(DirectoryParameterShouldHaveIgnore)
            .WithSpan(7, 54, 7, 60)
            .WithArguments("source");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task DirectoryParameter_WithIgnore_StillRequiresDefaultPath()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string Build([Ignore] Directory source) => "built";
            }
            """;

        var expected = VerifyCS
            .Diagnostic(DirectoryParameterShouldHaveDefaultPath)
            .WithSpan(7, 44, 7, 50)
            .WithArguments("source");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task NonDirectoryParameter_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string Build(string source) => "built";
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    #endregion

    #region DAGGER005: Directory parameter should consider [Ignore] attribute

    [TestMethod]
    public async Task DirectoryParameter_WithoutIgnore_ReportsDiagnostics()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string Build(Directory {|#0:source|}) => "built";
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(DirectoryParameterShouldHaveIgnore)
                .WithLocation(0)
                .WithArguments("source"),
            VerifyCS
                .Diagnostic(DirectoryParameterShouldHaveDefaultPath)
                .WithLocation(0)
                .WithArguments("source"),
        };

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task DirectoryParameter_WithIgnore_NoDiagnosticForDagger005()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string Build([Ignore] Directory source) => "built";
            }
            """;

        var expected = VerifyCS
            .Diagnostic(DirectoryParameterShouldHaveDefaultPath)
            .WithSpan(7, 44, 7, 50)
            .WithArguments("source");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task DirectoryParameter_WithDefaultPath_NoDiagnostic_ForIgnoreAnalyzer()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string Build([DefaultPath(".")] Directory source) => "built";
            }
            """;

        var expected = VerifyCS
            .Diagnostic(DirectoryParameterShouldHaveIgnore)
            .WithSpan(7, 54, 7, 60)
            .WithArguments("source");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task DirectoryParameter_WithDefaultPathAndIgnore_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string Build([DefaultPath(".")][Ignore] Directory source) => "built";
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    #endregion
}
