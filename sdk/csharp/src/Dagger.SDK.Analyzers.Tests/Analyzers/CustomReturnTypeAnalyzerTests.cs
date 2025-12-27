using static Dagger.SDK.Analyzers.DiagnosticDescriptors;
using VerifyCS = Dagger.SDK.Analyzers.Tests.Helpers.CSharpAnalyzerVerifier<Dagger.SDK.Analyzers.CustomReturnTypeAnalyzer>;

namespace Dagger.SDK.Analyzers.Tests.Analyzers;

[TestClass]
public class CustomReturnTypeAnalyzerTests
{
    [TestMethod]
    public async Task CustomReturnType_WithoutObjectAttribute_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public InternalExample Test()
                {
                    return new InternalExample();
                }
            }

            public class InternalExample
            {
                public string Name { get; set; }
            }
            """;

        var expected = VerifyCS
            .Diagnostic(CustomReturnTypeMissingObjectAttribute)
            .WithLocation(7, 12)
            .WithArguments("InternalExample");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task CustomReturnType_WithObjectAttribute_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public InternalExample Test()
                {
                    return new InternalExample();
                }
            }

            [Object]
            public class InternalExample
            {
                [Function]
                public string Name { get; set; }
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task AsyncCustomReturnType_WithoutObjectAttribute_ReportsDiagnostic()
    {
        var test = """
            using Dagger;
            using System.Threading.Tasks;

            [Object]
            public class MyModule
            {
                [Function]
                public async Task<InternalExample> Test()
                {
                    return new InternalExample();
                }
            }

            public class InternalExample
            {
                public string Name { get; set; }
            }
            """;

        var expected = VerifyCS
            .Diagnostic(CustomReturnTypeMissingObjectAttribute)
            .WithSpan(8, 18, 8, 39)
            .WithArguments("InternalExample");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task ValueTaskCustomReturnType_WithoutObjectAttribute_ReportsDiagnostic()
    {
        var test = """
            using Dagger;
            using System.Threading.Tasks;

            [Object]
            public class MyModule
            {
                [Function]
                public ValueTask<InternalExample> Test()
                {
                    return new ValueTask<InternalExample>(new InternalExample());
                }
            }

            public class InternalExample
            {
                public string Name { get; set; }
            }
            """;

        var expected = VerifyCS
            .Diagnostic(CustomReturnTypeMissingObjectAttribute)
            .WithSpan(8, 12, 8, 38)
            .WithArguments("InternalExample");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task PrimitiveReturnType_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string Test()
                {
                    return "hello";
                }
                
                [Function]
                public int GetNumber()
                {
                    return 42;
                }
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task DaggerTypeReturnType_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public Container Test()
                {
                    return new Container();
                }
                
                [Function]
                public Directory GetDir()
                {
                    return new Directory();
                }
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task EnumReturnType_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public MyEnum Test()
                {
                    return MyEnum.Value1;
                }
            }

            public enum MyEnum
            {
                Value1,
                Value2
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task ListOfCustomType_WithoutObjectAttribute_ReportsDiagnostic()
    {
        var test = """
            using Dagger;
            using System.Collections.Generic;

            [Object]
            public class MyModule
            {
                [Function]
                public List<InternalExample> Test()
                {
                    return new List<InternalExample>();
                }
            }

            public class InternalExample
            {
                public string Name { get; set; }
            }
            """;

        var expected = VerifyCS
            .Diagnostic(CustomReturnTypeMissingObjectAttribute)
            .WithLocation(8, 12)
            .WithArguments("InternalExample");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task ArrayOfCustomType_WithoutObjectAttribute_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public InternalExample[] Test()
                {
                    return new InternalExample[0];
                }
            }

            public class InternalExample
            {
                public string Name { get; set; }
            }
            """;

        var expected = VerifyCS
            .Diagnostic(CustomReturnTypeMissingObjectAttribute)
            .WithLocation(7, 12)
            .WithArguments("InternalExample");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task IEnumerableOfCustomType_WithoutObjectAttribute_ReportsDiagnostic()
    {
        var test = """
            using Dagger;
            using System.Collections.Generic;

            [Object]
            public class MyModule
            {
                [Function]
                public IEnumerable<InternalExample> Test()
                {
                    yield break;
                }
            }

            public class InternalExample
            {
                public string Name { get; set; }
            }
            """;

        var expected = VerifyCS
            .Diagnostic(CustomReturnTypeMissingObjectAttribute)
            .WithLocation(8, 12)
            .WithArguments("InternalExample");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task ListOfCustomType_WithObjectAttribute_NoDiagnostic()
    {
        var test = """
            using Dagger;
            using System.Collections.Generic;

            [Object]
            public class MyModule
            {
                [Function]
                public List<InternalExample> Test()
                {
                    return new List<InternalExample>();
                }
            }

            [Object]
            public class InternalExample
            {
                [Function]
                public string Name { get; set; }
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task NestedCustomReturnType_WithoutObjectAttribute_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public NestedType Test()
                {
                    return new NestedType();
                }
                
                public class NestedType
                {
                    public string Value { get; set; }
                }
            }
            """;

        var expected = VerifyCS
            .Diagnostic(CustomReturnTypeMissingObjectAttribute)
            .WithLocation(7, 12)
            .WithArguments("NestedType");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task NestedCustomReturnType_WithObjectAttribute_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public NestedType Test()
                {
                    return new NestedType();
                }
                
                [Object]
                public class NestedType
                {
                    [Function]
                    public string Value { get; set; }
                }
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task MethodWithoutFunctionAttribute_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                public InternalExample Test()
                {
                    return new InternalExample();
                }
            }

            public class InternalExample
            {
                public string Name { get; set; }
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task VoidReturnType_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public void Test()
                {
                }
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task TaskVoidReturnType_NoDiagnostic()
    {
        var test = """
            using Dagger;
            using System.Threading.Tasks;

            [Object]
            public class MyModule
            {
                [Function]
                public async Task Test()
                {
                }
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task SystemJsonTypes_NoDiagnostic()
    {
        var test = """
            using Dagger;
            using System.Text.Json;

            [Object]
            public class MyModule
            {
                [Function]
                public JsonElement Test()
                {
                    return default;
                }
                
                [Function]
                public JsonDocument GetDoc()
                {
                    return JsonDocument.Parse("{}");
                }
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }
}
