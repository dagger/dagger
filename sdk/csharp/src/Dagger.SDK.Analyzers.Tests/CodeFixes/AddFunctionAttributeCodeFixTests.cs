using static Dagger.SDK.Analyzers.DiagnosticDescriptors;
using VerifyCS = Dagger.SDK.Analyzers.Tests.Helpers.CSharpCodeFixVerifier<
    Dagger.SDK.Analyzers.DaggerObjectAnalyzer,
    Dagger.SDK.Analyzers.AddFunctionAttributeCodeFixProvider
>;

namespace Dagger.SDK.Analyzers.Tests.CodeFixes;

/// <summary>
/// Tests for AddFunctionAttributeCodeFixProvider (fixes DAGGER001).
/// </summary>
[TestClass]
public class AddFunctionAttributeCodeFixTests
{
    [TestMethod]
    public async Task AddFunctionAttribute_ToPublicMethod()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                public string {|#0:Hello|}() => "world";
            }
            """;

        var fixedCode = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string Hello() => "world";
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(PublicMethodInObjectMissingFunctionAttribute)
                .WithLocation(0)
                .WithArguments("Hello"),
            VerifyCS
                .Diagnostic(ObjectClassMissingXmlDocumentation)
                .WithSpan(4, 14, 4, 22)
                .WithArguments("MyModule"),
        };

        var fixedExpected = new[]
        {
            VerifyCS
                .Diagnostic(ObjectClassMissingXmlDocumentation)
                .WithSpan(4, 14, 4, 22)
                .WithArguments("MyModule"),
            VerifyCS
                .Diagnostic(FunctionMissingXmlDocumentation)
                .WithSpan(7, 19, 7, 24)
                .WithArguments("Hello"),
        };

        await VerifyCS.VerifyCodeFixAsync(test, expected, fixedExpected, fixedCode);
    }

    [TestMethod]
    public async Task AddFunctionAttribute_PreservesExistingAttributes()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Obsolete]
                public string {|#0:Hello|}() => "world";
            }
            """;

        var fixedCode = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Obsolete]
                [Function]
                public string Hello() => "world";
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(PublicMethodInObjectMissingFunctionAttribute)
                .WithLocation(0)
                .WithArguments("Hello"),
            VerifyCS
                .Diagnostic(ObjectClassMissingXmlDocumentation)
                .WithSpan(4, 14, 4, 22)
                .WithArguments("MyModule"),
        };

        var fixedExpected = new[]
        {
            VerifyCS
                .Diagnostic(ObjectClassMissingXmlDocumentation)
                .WithSpan(4, 14, 4, 22)
                .WithArguments("MyModule"),
            VerifyCS
                .Diagnostic(FunctionMissingXmlDocumentation)
                .WithSpan(8, 19, 8, 24)
                .WithArguments("Hello"),
        };

        await VerifyCS.VerifyCodeFixAsync(test, expected, fixedExpected, fixedCode);
    }

    [TestMethod]
    public async Task AddFunctionAttribute_WithParameters()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                public string {|#0:Greet|}(string name) => $"Hello, {name}";
            }
            """;

        var fixedCode = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string Greet(string name) => $"Hello, {name}";
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(PublicMethodInObjectMissingFunctionAttribute)
                .WithLocation(0)
                .WithArguments("Greet"),
            VerifyCS
                .Diagnostic(ObjectClassMissingXmlDocumentation)
                .WithSpan(4, 14, 4, 22)
                .WithArguments("MyModule"),
        };

        var fixedExpected = new[]
        {
            VerifyCS
                .Diagnostic(ObjectClassMissingXmlDocumentation)
                .WithSpan(4, 14, 4, 22)
                .WithArguments("MyModule"),
            VerifyCS
                .Diagnostic(FunctionMissingXmlDocumentation)
                .WithSpan(7, 19, 7, 24)
                .WithArguments("Greet"),
            VerifyCS
                .Diagnostic(ParameterMissingXmlDocumentation)
                .WithSpan(7, 32, 7, 36)
                .WithArguments("name"),
        };

        await VerifyCS.VerifyCodeFixAsync(test, expected, fixedExpected, fixedCode);
    }
}
