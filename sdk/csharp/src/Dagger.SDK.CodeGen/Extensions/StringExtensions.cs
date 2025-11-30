using System.Globalization;
using System.Linq;
using System.Text.RegularExpressions;

namespace Dagger.SDK.CodeGen.Extensions;

public static class StringExtensions
{
    public static string ToPascalCase(this string input)
    {
        // Insert a space before each uppercase letter that is preceded by a lowercase letter
        input = Regex.Replace(input, "(?<=\\p{Ll})(?=\\p{Lu})", " ");

        // Convert the entire string to lowercase
        input = input.ToLowerInvariant();

        // Split the string into words
        var words = Regex.Split(input, @"[\s_]+");

        // Capitalize the first letter of each word and join them
        return string.Concat(
            words.Select(static word => CultureInfo.CurrentCulture.TextInfo.ToTitleCase(word))
        );
    }
}
