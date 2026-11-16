using System.Globalization;
using Dagger.GraphQL;

namespace Dagger.SDK.Tests.GraphQL;

[TestClass]
public class ValueFormattingTest
{
    [TestMethod]
    public async Task TestStringValue_EscapesControlCharacters()
    {
        var formatted = await new StringValue("a\u001b[31mb\u0000c\bd\fe").FormatAsync();

        Assert.AreEqual("\"a\\u001b[31mb\\u0000c\\u0008d\\u000ce\"", formatted);
    }

    [TestMethod]
    public async Task TestIntValue_IgnoresCurrentCulture()
    {
        var previous = CultureInfo.CurrentCulture;
        CultureInfo.CurrentCulture = new CultureInfo("sv-SE");
        try
        {
            Assert.AreEqual("-5", await new IntValue(-5).FormatAsync());
        }
        finally
        {
            CultureInfo.CurrentCulture = previous;
        }
    }
}
