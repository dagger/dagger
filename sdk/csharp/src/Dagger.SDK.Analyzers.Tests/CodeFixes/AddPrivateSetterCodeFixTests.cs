using static Dagger.SDK.Analyzers.DiagnosticDescriptors;
using VerifyCS = Dagger.SDK.Analyzers.Tests.Helpers.CSharpCodeFixVerifier<
    Dagger.SDK.Analyzers.DaggerObjectAnalyzer,
    Dagger.SDK.Analyzers.AddPrivateSetterCodeFixProvider
>;

namespace Dagger.SDK.Analyzers.Tests.CodeFixes;

/// <summary>
/// Tests for AddPrivateSetterCodeFixProvider (fixes DAGGER022).
/// </summary>
[TestClass]
public class AddPrivateSetterCodeFixTests
{
    [TestMethod]
    public async Task AddPrivateSetter_ToFieldProperty()
    {
        var test = """
            using Dagger;

            /// <summary>My module</summary>
            [Object]
            public class MyModule
            {
                /// <summary>My field</summary>
                [Function]
                public string {|#0:MyField|} { get; }
            }
            """;

        var fixedCode = """
            using Dagger;

            /// <summary>My module</summary>
            [Object]
            public class MyModule
            {
                /// <summary>My field</summary>
                [Function]
                public string MyField { get; private set; }
            }
            """;

        var expected = VerifyCS
            .Diagnostic(FieldPropertyMustHaveSetter)
            .WithLocation(0)
            .WithArguments("MyField");

        await VerifyCS.VerifyCodeFixAsync(test, expected, fixedCode);
    }

    [TestMethod]
    public async Task AddPrivateSetter_ToFieldPropertyWithInitializer()
    {
        var test = """
            using Dagger;

            /// <summary>My module</summary>
            [Object]
            public class MyModule
            {
                /// <summary>My field</summary>
                [Function]
                public string {|#0:MyField|} { get; } = "default";
            }
            """;

        var fixedCode = """
            using Dagger;

            /// <summary>My module</summary>
            [Object]
            public class MyModule
            {
                /// <summary>My field</summary>
                [Function]
                public string MyField { get; private set; } = "default";
            }
            """;

        var expected = VerifyCS
            .Diagnostic(FieldPropertyMustHaveSetter)
            .WithLocation(0)
            .WithArguments("MyField");

        await VerifyCS.VerifyCodeFixAsync(test, expected, fixedCode);
    }

    [TestMethod]
    public async Task AddPrivateSetter_ToComplexProperty()
    {
        var test = """
            using Dagger;

            /// <summary>My module</summary>
            [Object]
            public class MyModule
            {
                private string _value = "test";

                /// <summary>My field</summary>
                [Function]
                public string {|#0:MyField|}
                {
                    get => _value;
                }
            }
            """;

        var fixedCode = """
            using Dagger;

            /// <summary>My module</summary>
            [Object]
            public class MyModule
            {
                private string _value = "test";

                /// <summary>My field</summary>
                [Function]
                public string MyField
                {
                    get => _value; private set;
                }
            }
            """;

        var expected = VerifyCS
            .Diagnostic(FieldPropertyMustHaveSetter)
            .WithLocation(0)
            .WithArguments("MyField");

        await VerifyCS.VerifyCodeFixAsync(test, expected, fixedCode);
    }
}
