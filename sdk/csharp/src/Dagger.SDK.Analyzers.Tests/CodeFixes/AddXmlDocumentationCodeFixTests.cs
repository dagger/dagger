using static Dagger.SDK.Analyzers.DiagnosticDescriptors;
using VerifyCS = Dagger.SDK.Analyzers.Tests.Helpers.CSharpCodeFixVerifier<
    Dagger.SDK.Analyzers.DaggerObjectAnalyzer,
    Dagger.SDK.Analyzers.AddXmlDocumentationCodeFixProvider
>;

namespace Dagger.SDK.Analyzers.Tests.CodeFixes;

/// <summary>
/// Tests for AddXmlDocumentationCodeFixProvider (fixes DAGGER002, DAGGER003, DAGGER006, DAGGER007).
/// </summary>
[TestClass]
public class AddXmlDocumentationCodeFixTests
{
    [TestMethod]
    public async Task AddXmlDocumentation_ToFunction()
    {
        var test = """
            using Dagger;

            /// <summary>My module</summary>
            [Object]
            public class MyModule
            {
                [Function]
                public string {|#0:Hello|}() => "world";
            }
            """;

        var fixedCode = """
            using Dagger;

            /// <summary>My module</summary>
            [Object]
            public class MyModule
            {
                /// <summary>
                /// TODO: Describe what Hello does
                /// </summary>
                [Function]
                public string Hello() => "world";
            }
            """;

        var expected = VerifyCS
            .Diagnostic(FunctionMissingXmlDocumentation)
            .WithLocation(0)
            .WithArguments("Hello");

        await VerifyCS.VerifyCodeFixAsync(test, expected, fixedCode);
    }

    [TestMethod]
    public async Task AddXmlDocumentation_ToObjectClass()
    {
        var test = """
            using Dagger;

            [Object]
            public class {|#0:MyModule|}
            {
                /// <summary>Hello function</summary>
                [Function]
                public string Hello() => "world";
            }
            """;

        var fixedCode = """
            using Dagger;
            /// <summary>
            /// TODO: Describe what MyModule does
            /// </summary>

            [Object]
            public class MyModule
            {
                /// <summary>Hello function</summary>
                [Function]
                public string Hello() => "world";
            }
            """;

        var expected = VerifyCS
            .Diagnostic(ObjectClassMissingXmlDocumentation)
            .WithLocation(0)
            .WithArguments("MyModule");

        await VerifyCS.VerifyCodeFixAsync(test, expected, fixedCode);
    }

    [TestMethod]
    public async Task AddXmlDocumentation_ToField()
    {
        var test = """
            using Dagger;

            /// <summary>My module</summary>
            [Object]
            public class MyModule
            {
                [Function]
                public string {|#0:Greeting|} { get; set; }
            }
            """;

        var fixedCode = """
            using Dagger;

            /// <summary>My module</summary>
            [Object]
            public class MyModule
            {
                /// <summary>
                /// TODO: Describe the Greeting field
                /// </summary>
                [Function]
                public string Greeting { get; set; }
            }
            """;

        var expected = VerifyCS
            .Diagnostic(FieldMissingXmlDocumentation)
            .WithLocation(0)
            .WithArguments("Greeting");

        await VerifyCS.VerifyCodeFixAsync(test, expected, fixedCode);
    }

    [TestMethod]
    public async Task AddXmlDocumentation_PreservesExistingIndentation()
    {
        var test = """
            using Dagger;

            /// <summary>My module</summary>
            [Object]
            public class MyModule
            {
                [Function]
                public string {|#0:Hello|}() => "world";

                /// <summary>Documented function</summary>
                [Function]
                public string Goodbye() => "bye";
            }
            """;

        var fixedCode = """
            using Dagger;

            /// <summary>My module</summary>
            [Object]
            public class MyModule
            {
                /// <summary>
                /// TODO: Describe what Hello does
                /// </summary>
                [Function]
                public string Hello() => "world";

                /// <summary>Documented function</summary>
                [Function]
                public string Goodbye() => "bye";
            }
            """;

        var expected = VerifyCS
            .Diagnostic(FunctionMissingXmlDocumentation)
            .WithLocation(0)
            .WithArguments("Hello");

        await VerifyCS.VerifyCodeFixAsync(test, expected, fixedCode);
    }
}
