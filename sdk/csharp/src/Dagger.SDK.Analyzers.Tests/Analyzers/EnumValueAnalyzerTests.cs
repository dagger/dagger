using static Dagger.SDK.Analyzers.DiagnosticDescriptors;
using VerifyCS = Dagger.SDK.Analyzers.Tests.Helpers.CSharpAnalyzerVerifier<Dagger.SDK.Analyzers.EnumValueAnalyzer>;

namespace Dagger.SDK.Analyzers.Tests.Analyzers;

/// <summary>
/// Tests for EnumValueAnalyzer (DAGGER015).
/// </summary>
[TestClass]
public class EnumValueAnalyzerTests
{
    #region DAGGER015: Enum member should have [EnumValue] attribute

    [TestMethod]
    public async Task EnumMemberWithoutEnumValueAttribute_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Enum]
            public enum Status
            {
                {|#0:Active|},
                {|#1:Inactive|}
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(EnumMemberMissingEnumValueAttribute)
                .WithLocation(0)
                .WithArguments("Active", "Status"),
            VerifyCS
                .Diagnostic(EnumMemberMissingEnumValueAttribute)
                .WithLocation(1)
                .WithArguments("Inactive", "Status"),
        };

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task EnumMemberWithEnumValueAttribute_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Enum]
            public enum Status
            {
                [EnumValue]
                Active,
                [EnumValue]
                Inactive
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task EnumMemberWithEnumValueAttributeAndProperties_NoDiagnostic()
    {
        var test = """
            using Dagger;

            [Enum]
            public enum LogLevel
            {
                [EnumValue(Description = "Debug level")]
                Debug,
                [EnumValue(Description = "Info level")]
                Info,
                [EnumValue(Description = "Warning level")]
                Warning,
                [EnumValue(Description = "Error level", Deprecated = "Use Critical instead")]
                Error
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task EnumWithoutEnumAttribute_NoDiagnostic()
    {
        var test = """
            namespace MyApp;

            public enum Status
            {
                Active,
                Inactive
            }
            """;

        await VerifyCS.VerifyAnalyzerAsync(test);
    }

    [TestMethod]
    public async Task MixedEnumMembersWithAndWithoutEnumValue_ReportsOnlyMissing()
    {
        var test = """
            using Dagger;

            [Enum]
            public enum Priority
            {
                [EnumValue(Description = "Low priority")]
                Low,
                {|#0:Medium|},
                [EnumValue(Description = "High priority")]
                High,
                {|#1:Critical|}
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(EnumMemberMissingEnumValueAttribute)
                .WithLocation(0)
                .WithArguments("Medium", "Priority"),
            VerifyCS
                .Diagnostic(EnumMemberMissingEnumValueAttribute)
                .WithLocation(1)
                .WithArguments("Critical", "Priority"),
        };

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task EnumWithExplicitValues_StillRequiresEnumValueAttribute()
    {
        var test = """
            using Dagger;

            [Enum]
            public enum ErrorCode
            {
                {|#0:NotFound|} = 404,
                {|#1:Unauthorized|} = 401,
                {|#2:BadRequest|} = 400
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(EnumMemberMissingEnumValueAttribute)
                .WithLocation(0)
                .WithArguments("NotFound", "ErrorCode"),
            VerifyCS
                .Diagnostic(EnumMemberMissingEnumValueAttribute)
                .WithLocation(1)
                .WithArguments("Unauthorized", "ErrorCode"),
            VerifyCS
                .Diagnostic(EnumMemberMissingEnumValueAttribute)
                .WithLocation(2)
                .WithArguments("BadRequest", "ErrorCode"),
        };

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task MultipleEnumsInSameFile_AnalyzesEachIndependently()
    {
        var test = """
            using Dagger;

            [Enum]
            public enum Status
            {
                [EnumValue]
                Active,
                [EnumValue]
                Inactive
            }

            [Enum]
            public enum Priority
            {
                {|#0:Low|},
                {|#1:High|}
            }

            public enum NotDaggerEnum
            {
                Value1,
                Value2
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(EnumMemberMissingEnumValueAttribute)
                .WithLocation(0)
                .WithArguments("Low", "Priority"),
            VerifyCS
                .Diagnostic(EnumMemberMissingEnumValueAttribute)
                .WithLocation(1)
                .WithArguments("High", "Priority"),
        };

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task EnumInNamespace_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            namespace MyApp.Models
            {
                [Enum]
                public enum Status
                {
                    {|#0:Active|},
                    {|#1:Inactive|}
                }
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(EnumMemberMissingEnumValueAttribute)
                .WithLocation(0)
                .WithArguments("Active", "Status"),
            VerifyCS
                .Diagnostic(EnumMemberMissingEnumValueAttribute)
                .WithLocation(1)
                .WithArguments("Inactive", "Status"),
        };

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task SingleMemberEnum_ReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Enum]
            public enum Result
            {
                {|#0:Success|}
            }
            """;

        var expected = VerifyCS
            .Diagnostic(EnumMemberMissingEnumValueAttribute)
            .WithLocation(0)
            .WithArguments("Success", "Result");

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    [TestMethod]
    public async Task EnumWithComments_StillReportsDiagnostic()
    {
        var test = """
            using Dagger;

            [Enum]
            public enum Status
            {
                /// <summary>Active status</summary>
                {|#0:Active|},
                
                /// <summary>Inactive status</summary>
                {|#1:Inactive|}
            }
            """;

        var expected = new[]
        {
            VerifyCS
                .Diagnostic(EnumMemberMissingEnumValueAttribute)
                .WithLocation(0)
                .WithArguments("Active", "Status"),
            VerifyCS
                .Diagnostic(EnumMemberMissingEnumValueAttribute)
                .WithLocation(1)
                .WithArguments("Inactive", "Status"),
        };

        await VerifyCS.VerifyAnalyzerAsync(test, expected);
    }

    #endregion
}
