using static Dagger.SDK.Analyzers.DiagnosticDescriptors;
using VerifyCS = Dagger.SDK.Analyzers.Tests.Helpers.CSharpCodeFixVerifier<
    Dagger.SDK.Analyzers.DaggerDirectoryAnalyzer,
    Dagger.SDK.Analyzers.AddIgnoreAttributeCodeFixProvider
>;

namespace Dagger.SDK.Analyzers.Tests.CodeFixes;

/// <summary>
/// Tests for AddIgnoreAttributeCodeFixProvider (fixes DAGGER005).
/// </summary>
[TestClass]
public class AddIgnoreAttributeCodeFixTests
{
    [TestMethod]
    public async Task AddIgnoreAttribute_ToDirectoryParameter()
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

        var fixedCode = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string Build([Ignore("node_modules", ".git")] Directory {|#0:source|}) => "built";
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

        var fixedExpected = new[]
        {
            VerifyCS
                .Diagnostic(DirectoryParameterShouldHaveDefaultPath)
                .WithLocation(0)
                .WithArguments("source"),
        };

        await VerifyCS.VerifyCodeFixAsync(test, expected, fixedExpected, fixedCode);
    }

    [TestMethod]
    public async Task AddIgnoreAttribute_PreservesExistingAttributes()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string Build([System.Diagnostics.CodeAnalysis.NotNull] Directory {|#0:source|}) => "built";
            }
            """;

        var fixedCode = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string Build([System.Diagnostics.CodeAnalysis.NotNull][Ignore("node_modules", ".git")] Directory {|#0:source|}) => "built";
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

        var fixedExpected = new[]
        {
            VerifyCS
                .Diagnostic(DirectoryParameterShouldHaveDefaultPath)
                .WithLocation(0)
                .WithArguments("source"),
        };

        await VerifyCS.VerifyCodeFixAsync(test, expected, fixedExpected, fixedCode);
    }
}
