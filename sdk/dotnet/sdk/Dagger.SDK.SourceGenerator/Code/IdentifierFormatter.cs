using System;
using System.Collections.Generic;
using System.Linq;
using Dagger.SDK.SourceGenerator.Types;

namespace Dagger.SDK.SourceGenerator.Code;

/// <summary>
/// How words are joined, as in the engine's IdentifierCasing.
/// </summary>
public enum Casing
{
    Pascal,
    Camel,
    Snake,
    ScreamingSnake,
    Kebab,
    Flat,
}

/// <summary>
/// How acronyms and terms are written where a word starts with a capital,
/// as in the engine's AcronymStyle.
/// </summary>
public enum AcronymStyle
{
    /// <summary>HTTPClient, IPv6Address, GitHubRepo</summary>
    Uppercase,

    /// <summary>HttpClient, Ipv6Address, GitHubRepo</summary>
    Capitalized,
}

/// <summary>
/// Formats the words the engine writes to the schema JSON's __identifiers,
/// exactly like the engine's formatIdentifiers (see
/// hack/designs/identifier-casing.md, "Schema JSON words").
/// </summary>
public static class IdentifierFormatter
{
    public const string KindWord = "WORD";
    public const string KindAcronym = "ACRONYM";
    public const string KindTerm = "TERM";

    /// <summary>
    /// Whether this generator knows how to format every word: an engine
    /// newer than it could add word kinds.
    /// </summary>
    public static bool CanFormat(IReadOnlyList<IdentifierWord> words) =>
        words.Count > 0
        && words.All(word =>
            (word.Kind is KindWord or KindAcronym or KindTerm) && word.Text.Length > 0
        );

    public static string Format(
        IReadOnlyList<IdentifierWord> words,
        Casing casing,
        AcronymStyle style
    )
    {
        switch (casing)
        {
            case Casing.Snake:
                return string.Join("_", words.Select(Lower));
            case Casing.ScreamingSnake:
                return string.Join("_", words.Select(Upper));
            case Casing.Kebab:
                return string.Join("-", words.Select(Lower));
            case Casing.Flat:
                return string.Concat(words.Select(Lower));
            case Casing.Pascal:
                return string.Concat(words.Select(word => CapitalizedForm(word, style)));
            case Casing.Camel:
                return string.Concat(
                    words.Select((word, i) => i == 0 ? Lower(word) : CapitalizedForm(word, style))
                );
            default:
                throw new ArgumentOutOfRangeException(nameof(casing), casing, null);
        }
    }

    private static string Lower(IdentifierWord word) =>
        (word.Text + word.Suffix).ToLowerInvariant();

    private static string Upper(IdentifierWord word) =>
        (word.Text + word.Suffix).ToUpperInvariant();

    private static string CapitalizedForm(IdentifierWord word, AcronymStyle style)
    {
        string form;
        if (style == AcronymStyle.Capitalized || word.Kind == KindWord)
        {
            form =
                word.Capitalized.Length > 0
                    ? word.Capitalized
                    : UpperFirst(word.Text.ToLowerInvariant());
        }
        else
        {
            form = UpperFirst(word.Text);
        }
        return form + word.Suffix;
    }

    private static string UpperFirst(string text) =>
        text.Length == 0 ? text : char.ToUpperInvariant(text[0]) + text.Substring(1);
}
