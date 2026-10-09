using System.Text.Json.Serialization;

namespace Dagger.SDK.SourceGenerator.Types;

/// <summary>
/// One word of a schema name, as the engine parsed it. Engines from v1.0.0
/// write the words of every name in the schema JSON's top-level
/// "__identifiers" map.
/// </summary>
public class IdentifierWord
{
    /// <summary>
    /// WORD, ACRONYM or TERM.
    /// </summary>
    [JsonPropertyName("kind")]
    public string Kind { get; set; } = "";

    /// <summary>
    /// The word's standard spelling: client, HTTP, GitHub.
    /// </summary>
    [JsonPropertyName("text")]
    public string Text { get; set; } = "";

    /// <summary>
    /// A plural "s" and/or trailing digits.
    /// </summary>
    [JsonPropertyName("suffix")]
    public string Suffix { get; set; } = "";

    /// <summary>
    /// The word in the CAPITALIZED acronym style, without the suffix: Client,
    /// Http, GitHub.
    /// </summary>
    [JsonPropertyName("capitalized")]
    public string Capitalized { get; set; } = "";
}
