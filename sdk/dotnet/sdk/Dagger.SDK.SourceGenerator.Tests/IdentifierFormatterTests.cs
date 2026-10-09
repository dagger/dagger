using System.Collections.Generic;
using System.Linq;
using System.Text.Json;
using System.Text.Json.Serialization;
using Dagger.SDK.SourceGenerator.Code;
using Dagger.SDK.SourceGenerator.Tests.Utils;
using Dagger.SDK.SourceGenerator.Types;
using Microsoft.VisualStudio.TestTools.UnitTesting;

namespace Dagger.SDK.SourceGenerator.Tests;

[TestClass]
public class IdentifierFormatterTests
{
    private class Vector
    {
        [JsonPropertyName("input")]
        public string Input { get; set; } = "";

        [JsonPropertyName("words")]
        public IdentifierWord[]? Words { get; set; }

        [JsonPropertyName("formats")]
        public Dictionary<string, string>? Formats { get; set; }
    }

    private static readonly Dictionary<string, (Casing, AcronymStyle)> VectorFormats = new()
    {
        ["PASCAL"] = (Casing.Pascal, AcronymStyle.Uppercase),
        ["PASCAL_CAPITALIZED"] = (Casing.Pascal, AcronymStyle.Capitalized),
        ["CAMEL"] = (Casing.Camel, AcronymStyle.Uppercase),
        ["CAMEL_CAPITALIZED"] = (Casing.Camel, AcronymStyle.Capitalized),
        ["SNAKE"] = (Casing.Snake, AcronymStyle.Uppercase),
        ["SCREAMING_SNAKE"] = (Casing.ScreamingSnake, AcronymStyle.Uppercase),
        ["KEBAB"] = (Casing.Kebab, AcronymStyle.Uppercase),
        ["FLAT"] = (Casing.Flat, AcronymStyle.Uppercase),
    };

    /// <summary>
    /// The engine's shared vectors, engine/naming/testdata/vectors.json.
    /// </summary>
    [TestMethod]
    public void FormatsSharedVectors()
    {
        var vectors = JsonSerializer.Deserialize<Vector[]>(Fixtures.Read("vectors.json"))!;
        var formatted = vectors.Where(vector => vector.Words is { Length: > 0 }).ToArray();
        Assert.IsTrue(formatted.Length > 0, "no shared vectors with words");

        foreach (var vector in formatted)
        {
            Assert.IsTrue(IdentifierFormatter.CanFormat(vector.Words!), vector.Input);
            Assert.AreEqual(VectorFormats.Count, vector.Formats!.Count, vector.Input);
            foreach (var (format, expected) in vector.Formats)
            {
                var (casing, style) = VectorFormats[format];
                Assert.AreEqual(
                    expected,
                    IdentifierFormatter.Format(vector.Words!, casing, style),
                    $"{vector.Input} as {format}"
                );
            }
        }
    }

    [TestMethod]
    public void CannotFormatUnknownWordKinds()
    {
        Assert.IsFalse(IdentifierFormatter.CanFormat([]));
        Assert.IsFalse(
            IdentifierFormatter.CanFormat([new IdentifierWord { Kind = "SYMBOL", Text = "x" }])
        );
    }
}
