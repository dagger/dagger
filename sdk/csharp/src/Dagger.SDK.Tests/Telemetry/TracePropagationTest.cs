using Dagger.Telemetry;

namespace Dagger.SDK.Tests.Telemetry;

[TestClass]
public class TracePropagationTest
{
    [TestInitialize]
    public void Setup()
    {
        TracePropagation.Reset();
    }
    [TestMethod]
    public void GetTraceParent_WhenEnvironmentVariableSet_ReturnsValue()
    {
        // Arrange
        const string expectedTraceParent = "00-12345678901234567890123456789012-1234567890123456-01";
        Environment.SetEnvironmentVariable("TRACEPARENT", expectedTraceParent);
        
        // Act
        TracePropagation.Initialize();
        var actual = TracePropagation.GetTraceParent();
        
        // Assert
        Assert.AreEqual(expectedTraceParent, actual);
        
        // Cleanup
        Environment.SetEnvironmentVariable("TRACEPARENT", null);
    }

    [TestMethod]
    public void GetTraceParent_WhenEnvironmentVariableNotSet_ReturnsNull()
    {
        // Arrange
        Environment.SetEnvironmentVariable("TRACEPARENT", null);
        
        // Act
        TracePropagation.Initialize();
        var actual = TracePropagation.GetTraceParent();
        
        // Assert
        Assert.IsNull(actual);
    }

    [TestMethod]
    public void Initialize_CalledMultipleTimes_OnlyExtractsOnce()
    {
        // Arrange
        const string expectedTraceParent = "00-abcdefabcdefabcdefabcdefabcdefab-abcdefabcdefab-01";
        Environment.SetEnvironmentVariable("TRACEPARENT", expectedTraceParent);
        
        // Act
        TracePropagation.Initialize();
        var first = TracePropagation.GetTraceParent();
        
        // Change env var after initialization
        Environment.SetEnvironmentVariable("TRACEPARENT", "different-value");
        TracePropagation.Initialize(); // Should not re-extract
        var second = TracePropagation.GetTraceParent();
        
        // Assert
        Assert.AreEqual(expectedTraceParent, first);
        Assert.AreEqual(expectedTraceParent, second, "Should return cached value, not re-extract");
        
        // Cleanup
        Environment.SetEnvironmentVariable("TRACEPARENT", null);
    }
}
