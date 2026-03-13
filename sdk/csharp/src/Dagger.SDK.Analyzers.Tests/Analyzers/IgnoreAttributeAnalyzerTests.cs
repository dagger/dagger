using static Dagger.SDK.Analyzers.DiagnosticDescriptors;
using VerifyCS = Dagger.SDK.Analyzers.Tests.Helpers.CSharpAnalyzerVerifier<Dagger.SDK.Analyzers.IgnoreAttributeAnalyzer>;

namespace Dagger.SDK.Analyzers.Tests.Analyzers;

/// <summary>
/// Tests for IgnoreAttributeAnalyzer (DAGGER009-010).
/// </summary>
[TestClass]
public class IgnoreAttributeAnalyzerTests
{
    #region DAGGER009: [Ignore] attribute can only be applied to Directory parameters

    [TestMethod]
    public async Task IgnoreAttribute_OnNonDirectoryParameter_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string Build([{|#0:Ignore|}] string source) => "built";
            }
            """;

        var expected = VerifyCS
            .Diagnostic(IgnoreAttributeOnInvalidParameterType)
            .WithLocation(0)
            .WithArguments("source", "String");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task IgnoreAttribute_OnDirectoryParameter_NoDiagnostic()
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

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task IgnoreAttribute_OnContainerParameter_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string Build([{|#0:Ignore|}] Container ctr) => "built";
            }
            """;

        var expected = VerifyCS
            .Diagnostic(IgnoreAttributeOnInvalidParameterType)
            .WithLocation(0)
            .WithArguments("ctr", "Container");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    #endregion

    #region DAGGER010: [DefaultPath] can only be applied to Directory or File parameters

    [TestMethod]
    public async Task DefaultPathAttribute_OnStringParameter_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string Build([{|#0:DefaultPath(".")|}] string source) => "built";
            }
            """;

        var expected = VerifyCS
            .Diagnostic(DefaultPathAttributeOnInvalidParameterType)
            .WithLocation(0)
            .WithArguments("source", "String");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task DefaultPathAttribute_OnDirectoryParameter_NoDiagnostic()
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

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task DefaultPathAttribute_OnFileParameter_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string Build([DefaultPath("README.md")] File readme) => "built";
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task DefaultPathAttribute_OnContainerParameter_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string Build([{|#0:DefaultPath(".")|}] Container ctr) => "built";
            }
            """;

        var expected = VerifyCS
            .Diagnostic(DefaultPathAttributeOnInvalidParameterType)
            .WithLocation(0)
            .WithArguments("ctr", "Container");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    #endregion
}
