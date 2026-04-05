using static Dagger.SDK.Analyzers.DiagnosticDescriptors;
using VerifyCS = Dagger.SDK.Analyzers.Tests.Helpers.CSharpCodeFixVerifier<
    Dagger.SDK.Analyzers.EnumValueAnalyzer,
    Dagger.SDK.Analyzers.AddEnumValueAttributeCodeFixProvider
>;

namespace Dagger.SDK.Analyzers.Tests.CodeFixes;

/// <summary>
/// Tests for AddEnumValueAttributeCodeFixProvider (fixes DAGGER015).
/// </summary>
[TestClass]
public class AddEnumValueAttributeCodeFixTests
{
    [TestMethod]
    public async Task AddEnumValueAttribute_ToEnumMember()
    {
        var test = """
            using Dagger;

            [Enum]
            public enum MyEnum
            {
                {|#0:Value1|},
                {|#1:Value2|}
            }
            """;

        var fixedCode = """
            using Dagger;

            [Enum]
            public enum MyEnum
            {
                [EnumValue]
                Value1,
                [EnumValue]
                Value2
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(EnumMemberMissingEnumValueAttribute)
                .WithLocation(0)
                .WithArguments("Value1", "MyEnum"),
            VerifyCS
                .Diagnostic(EnumMemberMissingEnumValueAttribute)
                .WithLocation(1)
                .WithArguments("Value2", "MyEnum"),
        };

        await VerifyCS.VerifyCodeFixAsync(test, expected, fixedCode);
    }

    [TestMethod]
    public async Task AddEnumValueAttribute_PreservesExistingAttributes()
    {
        var test = """
            using Dagger;

            [Enum]
            public enum MyEnum
            {
                [Obsolete]
                {|#0:Value1|},
                {|#1:Value2|}
            }
            """;

        var fixedCode = """
            using Dagger;

            [Enum]
            public enum MyEnum
            {
                [Obsolete]
                [EnumValue]
                Value1,
                [EnumValue]
                Value2
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(EnumMemberMissingEnumValueAttribute)
                .WithLocation(0)
                .WithArguments("Value1", "MyEnum"),
            VerifyCS
                .Diagnostic(EnumMemberMissingEnumValueAttribute)
                .WithLocation(1)
                .WithArguments("Value2", "MyEnum"),
        };

        await VerifyCS.VerifyCodeFixAsync(test, expected, fixedCode);
    }

    [TestMethod]
    public async Task AddEnumValueAttribute_WithExplicitValue()
    {
        var test = """
            using Dagger;

            [Enum]
            public enum MyEnum
            {
                {|#0:Value1|} = 1,
                {|#1:Value2|} = 2
            }
            """;

        var fixedCode = """
            using Dagger;

            [Enum]
            public enum MyEnum
            {
                [EnumValue]
                Value1 = 1,
                [EnumValue]
                Value2 = 2
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(EnumMemberMissingEnumValueAttribute)
                .WithLocation(0)
                .WithArguments("Value1", "MyEnum"),
            VerifyCS
                .Diagnostic(EnumMemberMissingEnumValueAttribute)
                .WithLocation(1)
                .WithArguments("Value2", "MyEnum"),
        };

        await VerifyCS.VerifyCodeFixAsync(test, expected, fixedCode);
    }
}
