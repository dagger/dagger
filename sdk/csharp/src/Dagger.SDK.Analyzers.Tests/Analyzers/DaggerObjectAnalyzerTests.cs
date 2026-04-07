using static Dagger.SDK.Analyzers.DiagnosticDescriptors;
using VerifyCS = Dagger.SDK.Analyzers.Tests.Helpers.CSharpAnalyzerVerifier<Dagger.SDK.Analyzers.DaggerObjectAnalyzer>;

namespace Dagger.SDK.Analyzers.Tests.Analyzers;

/// <summary>
/// Tests for DaggerObjectAnalyzer (DAGGER001-003, DAGGER006-008).
/// </summary>
[TestClass]
public class DaggerObjectAnalyzerTests
{
    #region DAGGER001: Public method should have [Function] attribute

    [TestMethod]
    public async Task PublicMethod_WithoutFunctionAttribute_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                public string {|#0:Hello|}() => "world";
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(ObjectClassMissingXmlDocumentation)
                .WithSpan(4, 14, 4, 22)
                .WithArguments("MyModule"),
            VerifyCS
                .Diagnostic(PublicMethodInObjectMissingFunctionAttribute)
                .WithLocation(0)
                .WithArguments("Hello"),
        };

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task PublicMethod_WithFunctionAttribute_NoDiagnostic()
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

        var expected = new[]
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

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task PrivateMethod_WithoutFunctionAttribute_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                private string Hello() => "world";
            }
            """;

        var expected = VerifyCS
            .Diagnostic(ObjectClassMissingXmlDocumentation)
            .WithSpan(4, 14, 4, 22)
            .WithArguments("MyModule");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task NonObjectClass_PublicMethod_NoDiagnostic()
    {
        var test = """
            public class MyClass
            {
                public string Hello() => "world";
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    #endregion

    #region DAGGER002: Function should have XML documentation

    [TestMethod]
    public async Task FunctionWithoutXmlDoc_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string {|#0:Hello|}() => "world";
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
                .WithLocation(0)
                .WithArguments("Hello"),
        };

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task FunctionWithXmlDoc_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                /// <summary>
                /// Returns a greeting.
                /// </summary>
                [Function]
                public string Hello() => "world";
            }
            """;

        var expected = VerifyCS
            .Diagnostic(ObjectClassMissingXmlDocumentation)
            .WithSpan(4, 14, 4, 22)
            .WithArguments("MyModule");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    #endregion

    #region DAGGER003: Function parameter should have XML documentation

    [TestMethod]
    public async Task FunctionParameter_WithoutXmlDoc_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                /// <summary>
                /// Returns a greeting.
                /// </summary>
                [Function]
                public string Hello(string {|#0:name|}) => $"Hello, {name}";
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(ObjectClassMissingXmlDocumentation)
                .WithSpan(4, 14, 4, 22)
                .WithArguments("MyModule"),
            VerifyCS
                .Diagnostic(ParameterMissingXmlDocumentation)
                .WithLocation(0)
                .WithArguments("name"),
        };

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task FunctionParameter_WithXmlDoc_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                /// <summary>
                /// Returns a greeting.
                /// </summary>
                /// <param name="name">The name to greet.</param>
                [Function]
                public string Hello(string name) => $"Hello, {name}";
            }
            """;

        var expected = VerifyCS
            .Diagnostic(ObjectClassMissingXmlDocumentation)
            .WithSpan(4, 14, 4, 22)
            .WithArguments("MyModule");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    #endregion

    #region DAGGER006: Object should have XML documentation

    [TestMethod]
    public async Task ObjectWithoutXmlDoc_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class {|#0:MyModule|}
            {
                [Function]
                public string Hello() => "world";
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(ObjectClassMissingXmlDocumentation)
                .WithLocation(0)
                .WithArguments("MyModule"),
            VerifyCS
                .Diagnostic(FunctionMissingXmlDocumentation)
                .WithSpan(7, 19, 7, 24)
                .WithArguments("Hello"),
        };

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task ObjectWithXmlDoc_NoDiagnostic()
    {
        var test = """
            using Dagger;

            /// <summary>
            /// A sample Dagger module.
            /// </summary>
            [Object]
            public class MyModule
            {
                /// <summary>
                /// Returns a greeting.
                /// </summary>
                [Function]
                public string Hello() => "world";
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    #endregion

    #region DAGGER007: Field should have XML documentation

    [TestMethod]
    public async Task FieldWithoutXmlDoc_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string {|#0:Greeting|} { get; set; }
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(ObjectClassMissingXmlDocumentation)
                .WithSpan(4, 14, 4, 22)
                .WithArguments("MyModule"),
            VerifyCS
                .Diagnostic(FieldMissingXmlDocumentation)
                .WithLocation(0)
                .WithArguments("Greeting"),
        };

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task FieldWithXmlDoc_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                /// <summary>
                /// The greeting message.
                /// </summary>
                [Function]
                public string Greeting { get; set; }
            }
            """;

        var expected = VerifyCS
            .Diagnostic(ObjectClassMissingXmlDocumentation)
            .WithSpan(4, 14, 4, 22)
            .WithArguments("MyModule");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    #endregion

    #region DAGGER008: Invalid Cache value in Function attribute

    [TestMethod]
    public async Task InvalidCacheValue_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function(Cache = {|#0:"invalid"|})]
                public string Hello() => "world";
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
                .WithSpan(7, 19, 7, 24)
                .WithArguments("Hello"),
        };

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task ValidCacheValue_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function(Cache = "SHARED")]
                public string Hello() => "world";
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
                .WithSpan(7, 19, 7, 24)
                .WithArguments("Hello"),
        };

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task NoCacheValue_NoDiagnostic()
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

        var expected = new[]
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

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    #endregion

    #region DAGGER018: Multiple public constructors

    [TestMethod]
    public async Task ObjectClass_MultiplePublicConstructors_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            /// <summary>Module with multiple constructors</summary>
            [Object]
            public class {|#0:MyModule|}
            {
                public MyModule() { }
                public MyModule(string name) { }
                public MyModule(string name, int port) { }

                [Function]
                public string Hello() => "world";
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(MultiplePublicConstructors)
                .WithLocation(0)
                .WithArguments("MyModule", 3),
            VerifyCS
                .Diagnostic(FunctionMissingXmlDocumentation)
                .WithSpan(12, 19, 12, 24)
                .WithArguments("Hello"),
        };

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task ObjectClass_SinglePublicConstructor_NoDiagnostic()
    {
        var test = """
            using Dagger;

            /// <summary>Module with one constructor</summary>
            [Object]
            public class MyModule
            {
                public MyModule(string name) { }

                [Function]
                public string Hello() => "world";
            }
            """;

        var expected = VerifyCS
            .Diagnostic(FunctionMissingXmlDocumentation)
            .WithSpan(10, 19, 10, 24)
            .WithArguments("Hello");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task ObjectClass_PrivateConstructorsWithPublic_NoDiagnostic()
    {
        var test = """
            using Dagger;

            /// <summary>Module with factory pattern</summary>
            [Object]
            public class MyModule
            {
                private MyModule(string value) { }

                public MyModule(int value) { }

                [Function]
                public string Hello() => "world";
            }
            """;

        var expected = VerifyCS
            .Diagnostic(FunctionMissingXmlDocumentation)
            .WithSpan(12, 19, 12, 24)
            .WithArguments("Hello");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task ObjectClass_OnlyDefaultConstructor_NoDiagnostic()
    {
        var test = """
            using Dagger;

            /// <summary>Module with default constructor</summary>
            [Object]
            public class MyModule
            {
                public MyModule() { }

                [Function]
                public string Hello() => "world";
            }
            """;

        var expected = VerifyCS
            .Diagnostic(FunctionMissingXmlDocumentation)
            .WithSpan(10, 19, 10, 24)
            .WithArguments("Hello");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task ObjectClass_TwoPublicConstructors_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            /// <summary>Module</summary>
            [Object]
            public class {|#0:MyModule|}
            {
                public MyModule(string greeting = "Hello") { }
                public MyModule(string greeting, int port) { }

                [Function]
                public string Hello() => "world";
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(MultiplePublicConstructors)
                .WithLocation(0)
                .WithArguments("MyModule", 2),
            VerifyCS
                .Diagnostic(FunctionMissingXmlDocumentation)
                .WithSpan(11, 19, 11, 24)
                .WithArguments("Hello"),
        };

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    #endregion

    #region DAGGER022: Field property must have setter

    [TestMethod]
    public async Task FieldProperty_WithoutSetter_ReportsDiagnostic()
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

        var expected = VerifyCS
            .Diagnostic(FieldPropertyMustHaveSetter)
            .WithLocation(0)
            .WithArguments("MyField");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task FieldProperty_WithPrivateSetter_NoDiagnostic()
    {
        var test = """
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

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task FieldProperty_WithPublicSetter_NoDiagnostic()
    {
        var test = """
            using Dagger;

            /// <summary>My module</summary>
            [Object]
            public class MyModule
            {
                /// <summary>My field</summary>
                [Function]
                public string MyField { get; set; }
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task PropertyWithoutFunctionAttribute_WithoutSetter_NoDiagnostic()
    {
        var test = """
            using Dagger;

            /// <summary>My module</summary>
            [Object]
            public class MyModule
            {
                public string MyProperty { get; }
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    #endregion
}
