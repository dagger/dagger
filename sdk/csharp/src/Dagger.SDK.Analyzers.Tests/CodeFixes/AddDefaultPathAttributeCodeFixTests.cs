using static Dagger.SDK.Analyzers.DiagnosticDescriptors;
using VerifyCS = Dagger.SDK.Analyzers.Tests.Helpers.CSharpCodeFixVerifier<
    Dagger.SDK.Analyzers.DaggerDirectoryAnalyzer,
    Dagger.SDK.Analyzers.AddDefaultPathAttributeCodeFixProvider
>;

namespace Dagger.SDK.Analyzers.Tests.CodeFixes;

/// <summary>
/// Tests for AddDefaultPathAttributeCodeFixProvider (fixes DAGGER004).
/// </summary>
[TestClass]
public class AddDefaultPathAttributeCodeFixTests
{
    [TestMethod]
    public async Task AddDefaultPathAttribute_ToDirectoryParameter()
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
                public string Build([DefaultPath(".")] Directory {|#0:source|}) => "built";
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
                .Diagnostic(DirectoryParameterShouldHaveIgnore)
                .WithLocation(0)
                .WithArguments("source"),
        };

        await VerifyCS.VerifyCodeFixAsync(test, expected, fixedExpected, fixedCode);
    }

    [TestMethod]
    public async Task AddDefaultPathAttribute_PreservesExistingAttributes()
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
                public string Build([System.Diagnostics.CodeAnalysis.NotNull][DefaultPath(".")] Directory {|#0:source|}) => "built";
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
                .Diagnostic(DirectoryParameterShouldHaveIgnore)
                .WithLocation(0)
                .WithArguments("source"),
        };

        await VerifyCS.VerifyCodeFixAsync(test, expected, fixedExpected, fixedCode);
    }
}
