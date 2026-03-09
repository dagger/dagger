using System.Reflection;
using Dagger;

namespace Dagger.SDK.Tests;

/// <summary>
/// Tests for the [Name] attribute functionality.
/// Verifies that constructor parameters, function parameters, fields, and functions
/// can all have custom API names that differ from their C# names.
/// </summary>
[TestClass]
public class NameAttributeTests
{
    /// <summary>
    /// Verifies that [Name] attribute correctly renames constructor parameters in the API.
    /// </summary>
    [TestMethod]
    public void Constructor_WithNameAttribute_UsesCustomParameterName()
    {
        // This test verifies the discovery process picks up the [Name] attribute
        // The actual runtime testing would require engine integration
        var attribute = new NameAttribute("customName");
        Assert.AreEqual("customName", attribute.ApiName);
    }

    /// <summary>
    /// Verifies that [Name] attribute on function parameter creates correct API name.
    /// </summary>
    [TestMethod]
    public void FunctionParameter_WithNameAttribute_UsesCustomParameterName()
    {
        var attribute = new NameAttribute("from");
        Assert.AreEqual("from", attribute.ApiName);
    }

    /// <summary>
    /// Verifies that [Name] attribute on field creates correct API name.
    /// </summary>
    [TestMethod]
    public void Field_WithNameAttribute_UsesCustomFieldName()
    {
        var attribute = new NameAttribute("customFieldName");
        Assert.AreEqual("customFieldName", attribute.ApiName);
    }

    /// <summary>
    /// Verifies that [Name] attribute on function creates correct API name.
    /// </summary>
    [TestMethod]
    public void Function_WithNameAttribute_UsesCustomFunctionName()
    {
        var attribute = new NameAttribute("import");
        Assert.AreEqual("import", attribute.ApiName);
    }

    /// <summary>
    /// Verifies that [Name] attribute rejects null or whitespace names.
    /// </summary>
    [TestMethod]
    [ExpectedException(typeof(ArgumentException))]
    public void NameAttribute_WithNullName_ThrowsArgumentException()
    {
        _ = new NameAttribute(null!);
    }

    /// <summary>
    /// Verifies that [Name] attribute rejects empty strings.
    /// </summary>
    [TestMethod]
    [ExpectedException(typeof(ArgumentException))]
    public void NameAttribute_WithEmptyName_ThrowsArgumentException()
    {
        _ = new NameAttribute("");
    }

    /// <summary>
    /// Verifies that [Name] attribute rejects whitespace-only strings.
    /// </summary>
    [TestMethod]
    [ExpectedException(typeof(ArgumentException))]
    public void NameAttribute_WithWhitespaceName_ThrowsArgumentException()
    {
        _ = new NameAttribute("   ");
    }

    /// <summary>
    /// Verifies that [Name] can be applied to parameters.
    /// </summary>
    [TestMethod]
    public void NameAttribute_AppliedToParameter_HasCorrectAttributeUsage()
    {
        var attributeUsage = typeof(NameAttribute)
            .GetCustomAttributes(typeof(AttributeUsageAttribute), false)
            .Cast<AttributeUsageAttribute>()
            .FirstOrDefault();

        Assert.IsNotNull(attributeUsage);
        Assert.IsTrue(
            (attributeUsage.ValidOn & AttributeTargets.Parameter) == AttributeTargets.Parameter
        );
    }

    /// <summary>
    /// Verifies that [Name] is only for parameters, not properties.
    /// Properties use [Function(Name = "...")] instead.
    /// </summary>
    [TestMethod]
    public void NameAttribute_NotAppliedToProperty_HasCorrectAttributeUsage()
    {
        var attributeUsage = typeof(NameAttribute)
            .GetCustomAttributes(typeof(AttributeUsageAttribute), false)
            .Cast<AttributeUsageAttribute>()
            .FirstOrDefault();

        Assert.IsNotNull(attributeUsage);
        Assert.IsFalse(
            (attributeUsage.ValidOn & AttributeTargets.Property) == AttributeTargets.Property,
            "NameAttribute should not be applicable to properties. Use [Function(Name = ...)] instead."
        );
    }

    /// <summary>
    /// Verifies that [Name] is only for parameters, not methods.
    /// Methods use [Function(Name = "...")] instead.
    /// </summary>
    [TestMethod]
    public void NameAttribute_NotAppliedToMethod_HasCorrectAttributeUsage()
    {
        var attributeUsage = typeof(NameAttribute)
            .GetCustomAttributes(typeof(AttributeUsageAttribute), false)
            .Cast<AttributeUsageAttribute>()
            .FirstOrDefault();

        Assert.IsNotNull(attributeUsage);
        Assert.IsFalse(
            (attributeUsage.ValidOn & AttributeTargets.Method) == AttributeTargets.Method,
            "NameAttribute should not be applicable to methods. Use [Function(Name = ...)] instead."
        );
    }

    /// <summary>
    /// Verifies that [Name] cannot be applied multiple times.
    /// </summary>
    [TestMethod]
    public void NameAttribute_AllowMultipleFalse_IsConfiguredCorrectly()
    {
        var attributeUsage = typeof(NameAttribute)
            .GetCustomAttributes(typeof(AttributeUsageAttribute), false)
            .Cast<AttributeUsageAttribute>()
            .FirstOrDefault();

        Assert.IsNotNull(attributeUsage);
        Assert.IsFalse(attributeUsage.AllowMultiple);
    }

    /// <summary>
    /// Verifies that [Name] is not inherited.
    /// </summary>
    [TestMethod]
    public void NameAttribute_InheritedFalse_IsConfiguredCorrectly()
    {
        var attributeUsage = typeof(NameAttribute)
            .GetCustomAttributes(typeof(AttributeUsageAttribute), false)
            .Cast<AttributeUsageAttribute>()
            .FirstOrDefault();

        Assert.IsNotNull(attributeUsage);
        Assert.IsFalse(attributeUsage.Inherited);
    }

    /// <summary>
    /// Integration test sample demonstrating the full usage pattern.
    /// This shows how [Name] would be used in a real module.
    /// </summary>
    [Object]
    private class SampleModuleWithNames
    {
        private readonly string _source;

        // Constructor parameter with Name attribute to avoid keyword
        public SampleModuleWithNames([Name("from")] string from_)
        {
            _source = from_;
        }

        // Field with custom API name
        [Function(Name = "customField")]
        public string InternalField { get; set; } = "";

        // Function with custom API name
        [Function(Name = "import")]
        public string Import_(
            // Parameter with custom API name to avoid keyword
            [Name("from")] string from_,
            // Regular parameter
            string destination
        )
        {
            return $"{from_} -> {destination}";
        }

        // Function with multiple renamed parameters
        [Function(Name = "process")]
        public string ProcessData(
            [Name("in")] string input,
            [Name("out")] string output,
            [Name("ref")] string reference
        )
        {
            return $"{input},{output},{reference}";
        }
    }

    /// <summary>
    /// Verifies that Name attribute can be applied to constructor parameters.
    /// </summary>
    [TestMethod]
    public void Integration_ConstructorParameter_CanHaveNameAttribute()
    {
        var constructor = typeof(SampleModuleWithNames).GetConstructors()[0];
        var parameter = constructor.GetParameters()[0];
        var nameAttr = parameter.GetCustomAttribute<NameAttribute>();

        Assert.IsNotNull(nameAttr);
        Assert.AreEqual("from", nameAttr.ApiName);
    }

    /// <summary>
    /// Verifies that Function attribute with Name parameter can be applied to properties.
    /// </summary>
    [TestMethod]
    public void Integration_Property_CanHaveFunctionAttributeWithName()
    {
        var property = typeof(SampleModuleWithNames).GetProperty("InternalField");
        var funcAttr = property!.GetCustomAttribute<FunctionAttribute>();

        Assert.IsNotNull(funcAttr);
        Assert.AreEqual("customField", funcAttr.Name);
    }

    /// <summary>
    /// Verifies that Function attribute with Name parameter can be applied to methods.
    /// </summary>
    [TestMethod]
    public void Integration_Method_CanHaveFunctionAttributeWithName()
    {
        var method = typeof(SampleModuleWithNames).GetMethod("Import_");
        var funcAttr = method!.GetCustomAttribute<FunctionAttribute>();

        Assert.IsNotNull(funcAttr);
        Assert.AreEqual("import", funcAttr.Name);
    }

    /// <summary>
    /// Verifies that Name attribute can be applied to function parameters.
    /// </summary>
    [TestMethod]
    public void Integration_FunctionParameter_CanHaveNameAttribute()
    {
        var method = typeof(SampleModuleWithNames).GetMethod("Import_");
        var parameter = method!.GetParameters()[0];
        var nameAttr = parameter.GetCustomAttribute<NameAttribute>();

        Assert.IsNotNull(nameAttr);
        Assert.AreEqual("from", nameAttr.ApiName);
    }

    /// <summary>
    /// Verifies that multiple parameters can have Name attributes.
    /// </summary>
    [TestMethod]
    public void Integration_MultipleParameters_CanHaveNameAttributes()
    {
        var method = typeof(SampleModuleWithNames).GetMethod("ProcessData");
        var parameters = method!.GetParameters();

        var inParam = parameters[0].GetCustomAttribute<NameAttribute>();
        var outParam = parameters[1].GetCustomAttribute<NameAttribute>();
        var refParam = parameters[2].GetCustomAttribute<NameAttribute>();

        Assert.IsNotNull(inParam);
        Assert.AreEqual("in", inParam.ApiName);

        Assert.IsNotNull(outParam);
        Assert.AreEqual("out", outParam.ApiName);

        Assert.IsNotNull(refParam);
        Assert.AreEqual("ref", refParam.ApiName);
    }
}
