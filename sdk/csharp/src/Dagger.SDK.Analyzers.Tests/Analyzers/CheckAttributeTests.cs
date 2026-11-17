using static Dagger.SDK.Analyzers.DiagnosticDescriptors;
using VerifyCS = Dagger.SDK.Analyzers.Tests.Helpers.CSharpAnalyzerVerifier<Dagger.SDK.Analyzers.DaggerObjectAnalyzer>;

namespace Dagger.SDK.Analyzers.Tests.Analyzers;

/// <summary>
/// Tests for [Check] attribute functionality.
/// </summary>
[TestClass]
public class CheckAttributeTests
{
    [TestMethod]
    public async Task CheckFunction_WithoutParameters_IsValid()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                [Check]
                public void ValidateFormat()
                {
                    // Validation logic
                }
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(ObjectClassMissingXmlDocumentation)
                .WithSpan(4, 14, 4, 22)
                .WithArguments("MyModule"),
            VerifyCS
                .Diagnostic(FunctionMissingXmlDocumentation)
                .WithSpan(8, 17, 8, 31)
                .WithArguments("ValidateFormat"),
        };

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task CheckFunction_WithOptionalParameters_IsValid()
    {
        var test = """
            using Dagger;
            using System.Threading.Tasks;

            [Object]
            public class MyModule
            {
                [Function]
                [Check]
                public async Task ValidateTests([DefaultPath(".")] Directory source)
                {
                    // Validation logic with optional parameter
                }
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(ObjectClassMissingXmlDocumentation)
                .WithSpan(5, 14, 5, 22)
                .WithArguments("MyModule"),
            VerifyCS
                .Diagnostic(FunctionMissingXmlDocumentation)
                .WithSpan(9, 23, 9, 36)
                .WithArguments("ValidateTests"),
            VerifyCS
                .Diagnostic(ParameterMissingXmlDocumentation)
                .WithSpan(9, 66, 9, 72)
                .WithArguments("source"),
        };

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task CheckFunction_WithTaskReturnType_IsValid()
    {
        var test = """
            using Dagger;
            using System.Threading.Tasks;

            [Object]
            public class MyModule
            {
                [Function]
                [Check]
                public async Task Lint()
                {
                    // Async validation logic
                }
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(ObjectClassMissingXmlDocumentation)
                .WithSpan(5, 14, 5, 22)
                .WithArguments("MyModule"),
            VerifyCS
                .Diagnostic(FunctionMissingXmlDocumentation)
                .WithSpan(9, 23, 9, 27)
                .WithArguments("Lint"),
        };

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task CheckFunction_WithVoidReturnType_IsValid()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                [Check]
                public void ValidateFormat()
                {
                    // Validation logic
                }
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(ObjectClassMissingXmlDocumentation)
                .WithSpan(4, 14, 4, 22)
                .WithArguments("MyModule"),
            VerifyCS
                .Diagnostic(FunctionMissingXmlDocumentation)
                .WithSpan(8, 17, 8, 31)
                .WithArguments("ValidateFormat"),
        };

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task RegularFunction_WithoutCheckAttribute_IsValid()
    {
        var test = """
            using Dagger;
            using System.Threading.Tasks;

            [Object]
            public class MyModule
            {
                [Function]
                public async Task<Container> Build(Directory source)
                {
                    return await Dag.Container().From("alpine");
                }
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(ObjectClassMissingXmlDocumentation)
                .WithSpan(5, 14, 5, 22)
                .WithArguments("MyModule"),
            VerifyCS
                .Diagnostic(FunctionMissingXmlDocumentation)
                .WithSpan(8, 34, 8, 39)
                .WithArguments("Build"),
            VerifyCS
                .Diagnostic(ParameterMissingXmlDocumentation)
                .WithSpan(8, 50, 8, 56)
                .WithArguments("source"),
        };

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task CheckFunction_OnInterface_IsValid()
    {
        var test = """
            using Dagger;

            [Interface]
            public interface IValidator
            {
                [Function]
                [Check]
                void Validate();
            }
            """;

        // Interface check functions should work without diagnostics
        await VerifyCS.VerifyAnalyzerAsync(test);
    }
}
