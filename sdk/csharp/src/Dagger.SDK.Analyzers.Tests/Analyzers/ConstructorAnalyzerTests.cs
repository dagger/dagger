// <copyright file="ConstructorAnalyzerTests.cs" company="Dagger">
// Copyright (c) Dagger. All rights reserved.
// </copyright>

using System.Threading.Tasks;
using Microsoft.VisualStudio.TestTools.UnitTesting;
using static Dagger.SDK.Analyzers.DiagnosticDescriptors;
using VerifyCS = Dagger.SDK.Analyzers.Tests.Helpers.CSharpAnalyzerVerifier<Dagger.SDK.Analyzers.ConstructorAnalyzer>;

namespace Dagger.SDK.Analyzers.Tests.Analyzers;

[TestClass]
public class ConstructorAnalyzerTests
{
    [TestMethod]
    public async Task ValidStaticConstructorMethod_NoDiagnostic()
    {
        var test =
            @"
using Dagger;

[Object]
public class MyModule
{
    private MyModule() { }

    [Constructor]
    public static MyModule Create()
    {
        return new MyModule();
    }
}
";
        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task ValidAsyncConstructorMethod_NoDiagnostic()
    {
        var test =
            @"
using Dagger;
using System.Threading.Tasks;

[Object]
public class MyModule
{
    private MyModule() { }

    [Constructor]
    public static async Task<MyModule> CreateAsync()
    {
        await Task.Delay(10);
        return new MyModule();
    }
}
";
        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task ValidValueTaskConstructorMethod_NoDiagnostic()
    {
        var test =
            @"
using Dagger;
using System.Threading.Tasks;

[Object]
public class MyModule
{
    private MyModule() { }

    [Constructor]
    public static ValueTask<MyModule> CreateAsync()
    {
        return new ValueTask<MyModule>(new MyModule());
    }
}
";
        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task NonStaticConstructorMethod_ReportsDiagnostic()
    {
        var test =
            @"
using Dagger;

[Object]
public class MyModule
{
    [Constructor]
    public MyModule {|#0:Create|}()
    {
        return this;
    }
}
";
        var expected = VerifyCS
            .Diagnostic(ConstructorAttributeOnNonStaticMethod)
            .WithLocation(0)
            .WithArguments("Create");
        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task ConstructorMethodWrongReturnType_ReportsDiagnostic()
    {
        var test =
            @"
using Dagger;

[Object]
public class MyModule
{
    [Constructor]
    public static {|#0:string|} Create()
    {
        return ""test"";
    }
}
";
        var expected = VerifyCS
            .Diagnostic(ConstructorAttributeInvalidReturnType)
            .WithLocation(0)
            .WithArguments("Create", "string", "MyModule");
        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task ConstructorMethodWrongTaskReturnType_ReportsDiagnostic()
    {
        var test =
            @"
using Dagger;
using System.Threading.Tasks;

[Object]
public class MyModule
{
    [Constructor]
    public static {|#0:Task<string>|} CreateAsync()
    {
        return Task.FromResult(""test"");
    }
}
";
        var expected = VerifyCS
            .Diagnostic(ConstructorAttributeInvalidReturnType)
            .WithLocation(0)
            .WithArguments("CreateAsync", "System.Threading.Tasks.Task<string>", "MyModule");
        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task MultipleConstructorMethods_ReportsDiagnostic()
    {
        var test =
            @"
using Dagger;

[Object]
public class MyModule
{
    [Constructor]
    public static MyModule {|#0:Create|}()
    {
        return new MyModule();
    }

    [Constructor]
    public static MyModule {|#1:CreateAlternative|}()
    {
        return new MyModule();
    }
}
";
        var expected1 = VerifyCS
            .Diagnostic(MultipleConstructorAttributes)
            .WithLocation(0)
            .WithArguments("MyModule", 2);
        var expected2 = VerifyCS
            .Diagnostic(MultipleConstructorAttributes)
            .WithLocation(1)
            .WithArguments("MyModule", 2);
        await VerifyCS.VerifyAnalyzerAsync(test, expected1, expected2);
    }

    [TestMethod]
    public async Task ConstructorMethodWithParameters_NoDiagnostic()
    {
        var test =
            @"
using Dagger;

[Object]
public class MyModule
{
    private readonly string _value;

    private MyModule(string value)
    {
        _value = value;
    }

    [Constructor]
    public static MyModule Create(string value)
    {
        return new MyModule(value);
    }
}
";
        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task ConstructorMethodWithAsyncInitialization_NoDiagnostic()
    {
        var test =
            @"
using Dagger;
using System.Threading.Tasks;

[Object]
public class MyModule
{
    private readonly string _apiKey;

    private MyModule(string apiKey)
    {
        _apiKey = apiKey;
    }

    [Constructor]
    public static async Task<MyModule> CreateAsync(string secretName)
    {
        // Simulate fetching secret
        await Task.Delay(10);
        var apiKey = ""fetched-"" + secretName;
        return new MyModule(apiKey);
    }
}
";
        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task PrivateConstructorWithPublicConstructorMethod_NoDiagnostic()
    {
        var test =
            @"
using Dagger;

[Object]
public class MyModule
{
    private MyModule() { }

    [Constructor]
    public static MyModule Create()
    {
        return new MyModule();
    }
}
";
        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task ConstructorMethodReturningNull_NoDiagnostic()
    {
        var test =
            @"
using Dagger;

[Object]
public class MyModule
{
    [Constructor]
    public static MyModule? Create(bool shouldCreate)
    {
        return shouldCreate ? new MyModule() : null;
    }
}
";
        await VerifyCS.VerifyAnalyzerAsync(test);
    }
}
