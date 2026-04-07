using static Dagger.SDK.Analyzers.DiagnosticDescriptors;
using VerifyCS = Dagger.SDK.Analyzers.Tests.Helpers.CSharpAnalyzerVerifier<Dagger.SDK.Analyzers.CheckAttributeAnalyzer>;

namespace Dagger.SDK.Analyzers.Tests.Analyzers;

/// <summary>
/// Tests for CheckAttributeAnalyzer (DAGGER016).
/// </summary>
[TestClass]
public class CheckAttributeAnalyzerTests
{
    #region DAGGER016: Check function must not have required parameters

    [TestMethod]
    public async Task CheckFunction_WithRequiredParameter_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                [Check]
                public void {|#0:Validate|}(string requiredParam)
                {
                }
            }
            """;

        var expected = VerifyCS
            .Diagnostic(CheckFunctionWithRequiredParameters)
            .WithLocation(0)
            .WithArguments("Validate", "'requiredParam'");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task CheckFunction_WithMultipleRequiredParameters_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                [Check]
                public void {|#0:Validate|}(string param1, int param2, Directory param3)
                {
                }
            }
            """;

        var expected = VerifyCS
            .Diagnostic(CheckFunctionWithRequiredParameters)
            .WithLocation(0)
            .WithArguments("Validate", "'param1', 'param2', 'param3'");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task CheckFunction_WithNoParameters_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                [Check]
                public void Validate()
                {
                }
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task CheckFunction_WithOptionalParameter_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                [Check]
                public void Validate(string optionalParam = "default")
                {
                }
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task CheckFunction_WithNullableParameter_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                [Check]
                public void Validate(string? nullableParam)
                {
                }
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task CheckFunction_WithNullableValueType_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                [Check]
                public void Validate(int? nullableInt)
                {
                }
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task CheckFunction_WithDefaultPathParameter_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                [Check]
                public void Validate([DefaultPath(".")] Directory source)
                {
                }
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task CheckFunction_WithMixedParameters_ReportsOnlyRequired()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                [Check]
                public void {|#0:Validate|}(
                    string? optional1,
                    string required1,
                    int optionalInt = 5,
                    string required2,
                    [DefaultPath(".")] Directory contextual)
                {
                }
            }
            """;

        var expected = VerifyCS
            .Diagnostic(CheckFunctionWithRequiredParameters)
            .WithLocation(0)
            .WithArguments("Validate", "'required1', 'required2'");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task RegularFunction_WithRequiredParameters_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string Build(Directory source)
                {
                    return "built";
                }
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task CheckFunction_AsyncTask_WithRequiredParameter_ReportsDiagnostic()
    {
        var test = """
            using Dagger;
            using System.Threading.Tasks;

            [Object]
            public class MyModule
            {
                [Function]
                [Check]
                public async Task {|#0:Validate|}(string requiredParam)
                {
                    await Task.CompletedTask;
                }
            }
            """;

        var expected = VerifyCS
            .Diagnostic(CheckFunctionWithRequiredParameters)
            .WithLocation(0)
            .WithArguments("Validate", "'requiredParam'");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task CheckFunction_OnInterface_WithRequiredParameter_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Interface]
            public interface IValidator
            {
                [Function]
                [Check]
                void {|#0:Validate|}(string requiredParam);
            }
            """;

        var expected = VerifyCS
            .Diagnostic(CheckFunctionWithRequiredParameters)
            .WithLocation(0)
            .WithArguments("Validate", "'requiredParam'");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    #endregion

    #region DAGGER017: Check function invalid return type

    [TestMethod]
    public async Task CheckFunction_VoidReturn_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                [Check]
                public void Validate()
                {
                }
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task CheckFunction_TaskReturn_NoDiagnostic()
    {
        var test = """
            using Dagger;
            using System.Threading.Tasks;

            [Object]
            public class MyModule
            {
                [Function]
                [Check]
                public async Task Validate()
                {
                    await Task.CompletedTask;
                }
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task CheckFunction_ContainerReturn_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                [Check]
                public Container Validate()
                {
                    return Dag.Container().From("alpine:3").WithExec(["sh", "-c", "exit 0"]);
                }
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task CheckFunction_TaskContainerReturn_NoDiagnostic()
    {
        var test = """
            using Dagger;
            using System.Threading.Tasks;

            [Object]
            public class MyModule
            {
                [Function]
                [Check]
                public async Task<Container> Validate()
                {
                    return await Task.FromResult(
                        Dag.Container().From("alpine:3").WithExec(["sh", "-c", "exit 0"])
                    );
                }
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task CheckFunction_StringReturn_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                [Check]
                public {|#0:string|} Validate()
                {
                    return "result";
                }
            }
            """;

        var expected = VerifyCS
            .Diagnostic(DiagnosticDescriptors.CheckFunctionInvalidReturnType)
            .WithLocation(0)
            .WithArguments("Validate", "string");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task CheckFunction_TaskStringReturn_ReportsDiagnostic()
    {
        var test = """
            using Dagger;
            using System.Threading.Tasks;

            [Object]
            public class MyModule
            {
                [Function]
                [Check]
                public async {|#0:Task<string>|} Validate()
                {
                    return await Task.FromResult("result");
                }
            }
            """;

        var expected = VerifyCS
            .Diagnostic(DiagnosticDescriptors.CheckFunctionInvalidReturnType)
            .WithLocation(0)
            .WithArguments("Validate", "System.Threading.Tasks.Task<string>");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task CheckFunction_IntReturn_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                [Check]
                public {|#0:int|} Validate()
                {
                    return 0;
                }
            }
            """;

        var expected = VerifyCS
            .Diagnostic(DiagnosticDescriptors.CheckFunctionInvalidReturnType)
            .WithLocation(0)
            .WithArguments("Validate", "int");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task CheckFunction_DirectoryReturn_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                [Check]
                public {|#0:Directory|} Validate()
                {
                    return Dag.Directory();
                }
            }
            """;

        var expected = VerifyCS
            .Diagnostic(DiagnosticDescriptors.CheckFunctionInvalidReturnType)
            .WithLocation(0)
            .WithArguments("Validate", "Dagger.Directory");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task RegularFunction_StringReturn_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                public string Build()
                {
                    return "built";
                }
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    #endregion
}
