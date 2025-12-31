using System.Globalization;
using System.Linq;
using System.Text.RegularExpressions;

namespace Dagger.SDK.CodeGen.Extensions;

public static partial class StringExtensions
{
    private static readonly Regex CamelCaseBoundary = CamelCaseBoundaryRegex();

    private static readonly Regex WordSplitter = WordSplitterRegex();

    extension(string input)
    {
        /// <summary>
        /// Converts the string to PascalCase.
        /// </summary>
        public string ToPascalCase()
        {
            // Insert a space before each uppercase letter that is preceded by a lowercase letter
            var modified = CamelCaseBoundary.Replace(input, " ");

            // Convert the entire string to lowercase
            modified = modified.ToLowerInvariant();

            // Split the string into words
            var words = WordSplitter.Split(modified);

            // Capitalize the first letter of each word and join them
            return string.Concat(
                words.Select(static word => CultureInfo.InvariantCulture.TextInfo.ToTitleCase(word))
            );
        }
    }

    [GeneratedRegex("(?<=\\p{Ll})(?=\\p{Lu})")]
    private static partial Regex CamelCaseBoundaryRegex();

    [GeneratedRegex(@"[\s_]+")]
    private static partial Regex WordSplitterRegex();
}
