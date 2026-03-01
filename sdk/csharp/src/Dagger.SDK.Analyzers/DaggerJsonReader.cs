using System.Collections.Immutable;
using System.Text;
using Microsoft.CodeAnalysis;

namespace Dagger.SDK.Analyzers;

/// <summary>
/// Utility to locate and parse dagger.json configuration files for Dagger modules.
/// </summary>
public class DaggerJsonReader
{
    private const int MaxSearchDepth = 10;

    /// <summary>
    /// Represents parsed dagger.json configuration.
    /// </summary>
    public class DaggerConfig
    {
        public string Name { get; set; } = string.Empty;
        public string Source { get; set; } = ".";
        public string FilePath { get; set; } = string.Empty;
    }

    /// <summary>
    /// Finds dagger.json from AdditionalFiles.
    /// The dagger.json file should be included in the .csproj as:
    /// &lt;AdditionalFiles Include="dagger.json" /&gt; or
    /// &lt;AdditionalFiles Include="../dagger.json" /&gt;
    /// </summary>
    /// <param name="additionalFiles">Additional files from compilation</param>
    /// <returns>Parsed configuration or null if not found</returns>
    public static DaggerConfig? FindDaggerJson(ImmutableArray<AdditionalText> additionalFiles)
    {
        // Look through all additional files for dagger.json
        var daggerJsonFile = additionalFiles.FirstOrDefault(f =>
            Path.GetFileName(f.Path).Equals("dagger.json", StringComparison.OrdinalIgnoreCase)
        );

        if (daggerJsonFile == null)
        {
            return null;
        }

        return ParseDaggerJson(daggerJsonFile);
    }

    /// <summary>
    /// Parses dagger.json from AdditionalText and extracts name and source fields.
    /// </summary>
    private static DaggerConfig? ParseDaggerJson(AdditionalText additionalText)
    {
        try
        {
            var text = additionalText.GetText();
            if (text == null)
            {
                return null;
            }

            var json = text.ToString();

            var name = ExtractJsonStringField(json, "name");
            var source = ExtractJsonStringField(json, "source");

            if (string.IsNullOrWhiteSpace(name))
            {
                return null;
            }

            return new DaggerConfig { Name = name!, Source = source ?? "." };
        }
        catch
        {
            return null;
        }
    }

    /// <summary>
    /// Simple JSON string field extraction without full JSON parser.
    /// Handles basic cases for dagger.json parsing.
    /// </summary>
    private static string? ExtractJsonStringField(string json, string fieldName)
    {
        // Look for "fieldName": "value" pattern
        var pattern = $"\"{fieldName}\"";
        var index = json.IndexOf(pattern, StringComparison.Ordinal);

        if (index == -1)
        {
            return null;
        }

        // Find the colon after field name
        var colonIndex = json.IndexOf(':', index + pattern.Length);
        if (colonIndex == -1)
        {
            return null;
        }

        // Skip whitespace after colon
        var valueStart = colonIndex + 1;
        while (valueStart < json.Length && char.IsWhiteSpace(json[valueStart]))
        {
            valueStart++;
        }

        // Check if value is a string (starts with quote)
        if (valueStart >= json.Length || json[valueStart] != '"')
        {
            return null;
        }

        // Find closing quote
        var valueEnd = valueStart + 1;
        while (valueEnd < json.Length)
        {
            if (json[valueEnd] == '"' && (valueEnd == valueStart + 1 || json[valueEnd - 1] != '\\'))
            {
                return json.Substring(valueStart + 1, valueEnd - valueStart - 1);
            }
            valueEnd++;
        }

        return null;
    }

    /// <summary>
    /// Transforms dagger.json module name to C# PascalCase class name.
    /// Matches the Go implementation in toolchains/csharp-sdk-dev/main.go FormatName function.
    /// </summary>
    public static string FormatName(string name)
    {
        if (string.IsNullOrWhiteSpace(name))
        {
            return "DaggerModule";
        }

        var separators = new[] { '-', '_', ' ', '.' };
        var parts = name.Split(separators, StringSplitOptions.RemoveEmptyEntries);

        if (parts.Length == 0)
        {
            return "DaggerModule";
        }

        var result = new StringBuilder();

        foreach (var part in parts)
        {
            if (string.IsNullOrWhiteSpace(part))
            {
                continue;
            }

            var trimmed = part.Trim();
            if (trimmed.Length == 0)
            {
                continue;
            }

            result.Append(char.ToUpperInvariant(trimmed[0]));

            if (trimmed.Length > 1)
            {
                var remainder = trimmed.Substring(1);
                if (remainder.Length > 0 && remainder.All(char.IsUpper))
                {
                    result.Append(remainder.ToLowerInvariant());
                }
                else
                {
                    result.Append(remainder);
                }
            }
        }

        var formatted = result.ToString();

        formatted = formatted.TrimStart('0', '1', '2', '3', '4', '5', '6', '7', '8', '9');

        if (string.IsNullOrEmpty(formatted))
        {
            return "DaggerModule";
        }

        if (char.IsLower(formatted[0]))
        {
            formatted = char.ToUpperInvariant(formatted[0]) + formatted.Substring(1);
        }

        return formatted;
    }
}
