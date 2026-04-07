using System.Linq;
using System.Text.Json;
using Dagger.SDK.CodeGen.Types;

namespace Dagger.SDK.CodeGen.Extensions;

/// <summary>
/// Extension methods for working with GraphQL directives.
/// </summary>
public static class DirectiveExtensions
{
    extension(Directive[]? directives)
    {
        /// <summary>
        /// Checks if a type, field, enum value, or input value has a specific directive.
        /// </summary>
        /// <param name="directiveName">The name of the directive to check for (without @ symbol).</param>
        /// <returns>True if the directive is present, false otherwise.</returns>
        public bool HasDirective(string directiveName)
        {
            return directives?.Any(d => d.Name == directiveName) ?? false;
        }

        /// <summary>
        /// Gets a specific directive by name.
        /// </summary>
        /// <param name="directiveName">The name of the directive to retrieve (without @ symbol).</param>
        /// <returns>The directive if found, null otherwise.</returns>
        public Directive? GetDirective(string directiveName)
        {
            return directives?.FirstOrDefault(d => d.Name == directiveName);
        }

        /// <summary>
        /// Checks if an element has the @experimental directive.
        /// </summary>
        /// <returns>True if the @experimental directive is present.</returns>
        public bool IsExperimental()
        {
            return directives.HasDirective("experimental");
        }

        /// <summary>
        /// Gets the reason for an experimental directive.
        /// </summary>
        /// <returns>The experimental reason, or null if no @experimental directive or no reason provided.</returns>
        public string? GetExperimentalReason()
        {
            var experimental = directives.GetDirective("experimental");
            return experimental.GetDirectiveArgument("reason");
        }
    }

    extension(Directive? directive)
    {
        /// <summary>
        /// Gets the value of a directive argument as a string.
        /// </summary>
        /// <param name="argumentName">The name of the argument to retrieve.</param>
        /// <returns>The argument value as a string, or null if not found.</returns>
        public string? GetDirectiveArgument(string argumentName)
        {
            var arg = directive?.Args?.FirstOrDefault(a => a.Name == argumentName);
            if (arg == null)
            {
                return null;
            }

            // Handle different JsonElement value types
            return arg.Value.ValueKind switch
            {
                JsonValueKind.String => arg.Value.GetString(),
                JsonValueKind.Number => arg.Value.ToString(),
                JsonValueKind.True => "true",
                JsonValueKind.False => "false",
                JsonValueKind.Null => null,
                _ => arg.Value.GetRawText(),
            };
        }
    }
}
