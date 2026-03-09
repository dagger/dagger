using static Dagger.SDK.Analyzers.DiagnosticDescriptors;
using VerifyCS = Dagger.SDK.Analyzers.Tests.Helpers.CSharpCodeFixVerifier<
    Dagger.SDK.Analyzers.ModuleConfigurationAnalyzer,
    Dagger.SDK.Analyzers.RenameModuleClassCodeFixProvider
>;

namespace Dagger.SDK.Analyzers.Tests.CodeFixes;

/// <summary>
/// Tests for RenameModuleClassCodeFixProvider (fixes DAGGER012).
/// </summary>
[TestClass]
public class RenameModuleClassCodeFixTests
{
    [TestMethod]
    public async Task RenameClass_ToMatchDaggerJson()
    {
        var test = """
            using Dagger;

            [Object]
            public class {|#0:WrongName|}
            {
                [Function]
                public string Hello() => "world";
            }
            """;

        var fixedCode = """
            using Dagger;

            [Object]
            public class TestProject
            {
                [Function]
                public string Hello() => "world";
            }
            """;

        var additionalFiles = new[]
        {
            (
                "dagger.json",
                """
                {
                  "name": "test-project",
                  "source": "."
                }
                """
            ),
        };

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(ModuleClassNameMismatch)
                .WithLocation(0)
                .WithArguments("TestProject", "test-project"),
        };

        await VerifyCS.VerifyCodeFixAsync(
            test,
            additionalFiles,
            expected,
            fixedCode,
            assemblyName: "TestProject"
        );
    }

    [TestMethod]
    public async Task RenameClass_KebabToPascalCase()
    {
        var test = """
            using Dagger;

            [Object]
            public class {|#0:OldName|}
            {
                [Function]
                public string Hello() => "world";
            }
            """;

        var fixedCode = """
            using Dagger;

            [Object]
            public class Testprojectv2
            {
                [Function]
                public string Hello() => "world";
            }
            """;

        var additionalFiles = new[]
        {
            (
                "dagger.json",
                """
                {
                  "name": "testprojectv2",
                  "source": "."
                }
                """
            ),
        };

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(ModuleClassNameMismatch)
                .WithLocation(0)
                .WithArguments("Testprojectv2", "testprojectv2"),
        };

        await VerifyCS.VerifyCodeFixAsync(
            test,
            additionalFiles,
            expected,
            fixedCode,
            assemblyName: "Testprojectv2"
        );
    }
}
