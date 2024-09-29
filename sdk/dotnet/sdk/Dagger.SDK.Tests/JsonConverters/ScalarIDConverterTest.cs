using System.Text.Json;
using System.Text.Json.Serialization;
using Dagger.SDK.JsonConverters;

namespace Dagger.SDK.Tests;

[TestClass]
public class ScalarIDConverterTest
{
    [JsonConverter(typeof(ScalarIdConverter<DemoID>))]
    public class DemoID : Scalar { }

    [TestMethod]
    public void TestJsonSerialization()
    {
        var demoId = JsonSerializer.Deserialize<DemoID>("\"hello\"")!;
        Assert.AreEqual("hello", demoId.Value);
        Assert.AreEqual("\"hello\"", JsonSerializer.Serialize(demoId));
    }
}
