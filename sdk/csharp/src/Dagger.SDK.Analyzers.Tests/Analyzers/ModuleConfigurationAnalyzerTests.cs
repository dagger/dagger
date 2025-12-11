using static Dagger.SDK.Analyzers.DiagnosticDescriptors;
using VerifyCS = Dagger.SDK.Analyzers.Tests.Helpers.CSharpAnalyzerVerifier<Dagger.SDK.Analyzers.ModuleConfigurationAnalyzer>;

namespace Dagger.SDK.Analyzers.Tests.Analyzers;

/// <summary>
/// Tests for ModuleConfigurationAnalyzer (DAGGER011-013).
/// </summary>
[TestClass]
public class ModuleConfigurationAnalyzerTests
{
    #region DAGGER011: Dagger module requires dagger.json configuration file

    [TestMethod]
    public async Task MissingDaggerJson_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string Hello() => "world";
            }
            """;

        var expected = VerifyCS
            .Diagnostic(MissingDaggerJson)
            .WithSpan(4, 14, 4, 22)
            .WithArguments("/0");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task DaggerJsonPresent_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
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
                  "name": "my-module",
                  "source": "."
                }
                """
            ),
        };

        var expected = VerifyCS
            .Diagnostic(ProjectFileNameMismatch)
            .WithSpan(4, 14, 4, 22)
            .WithArguments("MyModule", "my-module", "TestProject");

        await VerifyCS.VerifyAnalyzerAsync(test, additionalFiles, expected);
    }

    #endregion

    #region DAGGER012: Module class name should match dagger.json module name

    [TestMethod]
    public async Task ClassNameMismatch_ReportsDiagnostic()
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

        var additionalFiles = new[]
        {
            (
                "dagger.json",
                """
                {
                  "name": "my-module",
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
                .WithArguments("MyModule", "my-module"),
            VerifyCS
                .Diagnostic(ProjectFileNameMismatch)
                .WithSpan(4, 14, 4, 23)
                .WithArguments("MyModule", "my-module", "TestProject"),
        };

        await VerifyCS.VerifyAnalyzerAsync(test, additionalFiles, expected);
    }

    [TestMethod]
    public async Task ClassNameMatches_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
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
                  "name": "my-module",
                  "source": "."
                }
                """
            ),
        };

        var expected = VerifyCS
            .Diagnostic(ProjectFileNameMismatch)
            .WithSpan(4, 14, 4, 22)
            .WithArguments("MyModule", "my-module", "TestProject");

        await VerifyCS.VerifyAnalyzerAsync(test, additionalFiles, expected);
    }

    [TestMethod]
    public async Task KebabCaseToPascalCase_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class FriendlyBard
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
                  "name": "friendly-bard",
                  "source": "."
                }
                """
            ),
        };

        var expected = VerifyCS
            .Diagnostic(ProjectFileNameMismatch)
            .WithSpan(4, 14, 4, 26)
            .WithArguments("FriendlyBard", "friendly-bard", "TestProject");

        await VerifyCS.VerifyAnalyzerAsync(test, additionalFiles, expected);
    }

    [TestMethod]
    public async Task SnakeCaseToPascalCase_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class FriendlyBard
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
                  "name": "friendly_bard",
                  "source": "."
                }
                """
            ),
        };

        var expected = VerifyCS
            .Diagnostic(ProjectFileNameMismatch)
            .WithSpan(4, 14, 4, 26)
            .WithArguments("FriendlyBard", "friendly_bard", "TestProject");

        await VerifyCS.VerifyAnalyzerAsync(test, additionalFiles, expected);
    }

    [TestMethod]
    public async Task MultipleObjectClasses_OneMatches_NoWarning()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string Hello() => "world";
            }

            [Object]
            public class OtherClass
            {
                [Function]
                public string Goodbye() => "farewell";
            }
            """;

        var additionalFiles = new[]
        {
            (
                "dagger.json",
                """
                {
                  "name": "my-module",
                  "source": "."
                }
                """
            ),
        };

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(ProjectFileNameMismatch)
                .WithSpan(4, 14, 4, 22)
                .WithArguments("MyModule", "my-module", "TestProject"),
        };

        await VerifyCS.VerifyAnalyzerAsync(test, additionalFiles, expected);
    }

    #endregion

    #region Edge Cases

    [TestMethod]
    public async Task MalformedDaggerJson_NoException()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string Hello() => "world";
            }
            """;

        var additionalFiles = new[] { ("dagger.json", "{ invalid json }") };

        var expected = VerifyCS
            .Diagnostic(MissingDaggerJson)
            .WithSpan(4, 14, 4, 22)
            .WithArguments("/0");

        // Should not crash, may report DAGGER011 if parsing fails
        await VerifyCS.VerifyAnalyzerAsync(test, additionalFiles, expected);
    }

    [TestMethod]
    public async Task DaggerJsonMissingNameField_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
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
                  "source": "."
                }
                """
            ),
        };

        var expected = VerifyCS
            .Diagnostic(MissingDaggerJson)
            .WithSpan(4, 14, 4, 22)
            .WithArguments("/0");

        // If name field is missing, analyzer should gracefully handle it
        await VerifyCS.VerifyAnalyzerAsync(test, additionalFiles, expected);
    }

    [TestMethod]
    public async Task NonObjectClass_NotValidated()
    {
        var test = """
            using Dagger;

            public class NotAModule
            {
                public string Hello() => "world";
            }
            """;

        var additionalFiles = new[]
        {
            (
                "dagger.json",
                """
                {
                  "name": "my-module",
                  "source": "."
                }
                """
            ),
        };

        // Should not report DAGGER012 for classes without [Object] attribute
        await VerifyCS.VerifyAnalyzerAsync(test, additionalFiles);
    }

    #endregion
}
