using static Dagger.SDK.Analyzers.DiagnosticDescriptors;
using VerifyCS = Dagger.SDK.Analyzers.Tests.Helpers.CSharpAnalyzerVerifier<Dagger.SDK.Analyzers.GenerateUpAttributeAnalyzer>;

namespace Dagger.SDK.Analyzers.Tests.Analyzers;

/// <summary>
/// Tests for GenerateUpAttributeAnalyzer (DAGGER024-DAGGER027).
/// </summary>
[TestClass]
public class GenerateUpAttributeAnalyzerTests
{
    [TestMethod]
    public async Task GenerateFunction_ReturningChangeset_NoDiagnostic()
    {
        var test = """
            using System.Threading.Tasks;
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                [Generate]
                public Changeset Codegen([DefaultPath("/")] Directory source) =>
                    source.WithNewFile("gen.txt", "hi").Changes(source);

                [Function]
                [Generate]
                public Task<Changeset> CodegenAsync(string? name = null) =>
                    Task.FromResult(Dag.Directory().Changes(Dag.Directory()));
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task GenerateFunction_WithRequiredParameter_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                [Generate]
                public Changeset {|#0:Codegen|}(Directory source) => source.Changes(source);
            }
            """;

        var expected = VerifyCS
            .Diagnostic(GenerateFunctionWithRequiredParameters)
            .WithLocation(0)
            .WithArguments("Codegen", "'source'");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task GenerateFunction_WithInvalidReturnType_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                [Generate]
                public {|#0:Directory|} Codegen() => Dag.Directory();
            }
            """;

        var expected = VerifyCS
            .Diagnostic(GenerateFunctionInvalidReturnType)
            .WithLocation(0)
            .WithArguments("Codegen", "Dagger.Directory");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task UpFunction_ReturningService_NoDiagnostic()
    {
        var test = """
            using System.Threading.Tasks;
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                [Up]
                public Service Web(int port = 80) =>
                    Dag.Container().From("nginx:alpine").WithExposedPort(port).AsService();

                [Function]
                [Up]
                public Task<Service> WebAsync() =>
                    Task.FromResult(Dag.Container().From("nginx:alpine").AsService());
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task UpFunction_WithRequiredParameter_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                [Up]
                public Service {|#0:Web|}(int port) =>
                    Dag.Container().From("nginx:alpine").WithExposedPort(port).AsService();
            }
            """;

        var expected = VerifyCS
            .Diagnostic(UpFunctionWithRequiredParameters)
            .WithLocation(0)
            .WithArguments("Web", "'port'");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task UpFunction_WithInvalidReturnType_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Object]
            public class MyModule
            {
                [Function]
                [Up]
                public {|#0:Container|} Web() => Dag.Container().From("nginx:alpine");
            }
            """;

        var expected = VerifyCS
            .Diagnostic(UpFunctionInvalidReturnType)
            .WithLocation(0)
            .WithArguments("Web", "Dagger.Container");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }
}
