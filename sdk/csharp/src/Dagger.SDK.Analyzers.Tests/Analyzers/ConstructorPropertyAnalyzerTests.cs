using System.Threading.Tasks;
using Microsoft.CodeAnalysis.Testing;
using Microsoft.VisualStudio.TestTools.UnitTesting;
using VerifyCS = Dagger.SDK.Analyzers.Tests.Helpers.CSharpAnalyzerVerifier<Dagger.SDK.Analyzers.ConstructorPropertyAnalyzer>;

namespace Dagger.SDK.Analyzers.Tests.Analyzers;

[TestClass]
public class ConstructorPropertyAnalyzerTests
{
    [TestMethod]
    public async Task Constructor_AssignsToPrivateField_ProducesDiagnostic()
    {
        var test =
            @"
using Dagger;

[Object]
public class Test
{
    private readonly string _name;

    public Test(string {|#0:name|} = ""default"")
    {
        _name = name;
    }

    [Function]
    public string GetName() => _name;
}";

        var expected = VerifyCS
            .Diagnostic(DiagnosticDescriptors.ConstructorParameterShouldMapToPublicProperty)
            .WithLocation(0)
            .WithArguments("name");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task Constructor_AssignsToPublicProperty_NoDiagnostic()
    {
        var test =
            @"
using Dagger;

[Object]
public class Test
{
    public string Name { get; set; } = ""default"";

    public Test(string name = ""default"")
    {
        Name = name;
    }

    [Function]
    public string GetName() => Name;
}";

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task Constructor_NoAssignmentToField_NoDiagnostic()
    {
        var test =
            @"
using Dagger;

[Object]
public class Test
{
    public string Name { get; set; } = ""default"";

    public Test(string notUsed = ""default"")
    {
        // Parameter not used - no assignment
    }

    [Function]
    public string GetName() => Name;
}";

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task NonObjectClass_NoAnalysis()
    {
        var test =
            @"
public class Test
{
    private readonly string _name;

    public Test(string name = ""default"")
    {
        _name = name;
    }
}";

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task Constructor_MultipleParameters_OnlyDiagnosticForPrivateFields()
    {
        var test =
            @"
using Dagger;

[Object]
public class Test
{
    private readonly string _name;
    public int Port { get; set; }

    public Test(string {|#0:name|} = ""default"", int port = 8080)
    {
        _name = name;
        Port = port;
    }

    [Function]
    public string GetName() => _name;
}";

        var expected = VerifyCS
            .Diagnostic(DiagnosticDescriptors.ConstructorParameterShouldMapToPublicProperty)
            .WithLocation(0)
            .WithArguments("name");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }
}
