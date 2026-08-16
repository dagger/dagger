using System.Diagnostics;

namespace Dagger.Telemetry;

/// <summary>
/// Extension methods and utilities for OpenTelemetry telemetry operations.
/// </summary>
internal static class TelemetryExtensions
{
    /// <summary>
    /// Parses a TRACEPARENT value into an ActivityContext.
    /// </summary>
    /// <param name="traceParent">The W3C TRACEPARENT string (format: 00-trace-id-parent-id-flags)</param>
    /// <returns>An ActivityContext if parsing succeeds, null otherwise.</returns>
    public static ActivityContext? ParseTraceParent(string? traceParent)
    {
        if (string.IsNullOrWhiteSpace(traceParent))
        {
            return null;
        }

        try
        {
            return ActivityContext.TryParse(traceParent, null, out var context) ? context : null;
        }
        catch
        {
            return null;
        }
    }

    /// <summary>
    /// Builds span attributes from function call arguments.
    /// </summary>
    /// <param name="functionName">The name of the function being called.</param>
    /// <param name="args">Dictionary of argument names and values.</param>
    /// <returns>A dictionary of span attributes.</returns>
    public static Dictionary<string, object?> BuildFunctionAttributes(
        string functionName,
        IDictionary<string, object?>? args = null
    )
    {
        var attributes = new Dictionary<string, object?>
        {
            ["dagger.function.name"] = functionName,
        };

        if (args != null)
        {
            foreach (var (name, value) in args)
            {
                // Sanitize sensitive values
                if (IsSensitiveParameter(name))
                {
                    attributes[$"dagger.function.arg.{name}"] = "[REDACTED]";
                }
                else
                {
                    attributes[$"dagger.function.arg.{name}"] = SanitizeValue(value);
                }
            }
        }

        return attributes;
    }

    /// <summary>
    /// Checks if a parameter name suggests it contains sensitive data.
    /// </summary>
    private static bool IsSensitiveParameter(string name)
    {
        var lowerName = name.ToLowerInvariant();
        return lowerName.Contains("secret")
            || lowerName.Contains("password")
            || lowerName.Contains("token")
            || lowerName.Contains("key")
            || lowerName.Contains("credential");
    }

    /// <summary>
    /// Sanitizes a value for inclusion in span attributes.
    /// Converts complex objects to simple string representations.
    /// </summary>
    private static object? SanitizeValue(object? value)
    {
        if (value == null)
        {
            return null;
        }

        // Keep primitive types as-is
        if (value is string or int or long or bool or double or float or decimal)
        {
            return value;
        }

        // Convert complex types to string representation
        // Limit length to avoid excessive attribute sizes
        var str = value.ToString() ?? "";
        return str.Length > 200 ? str.Substring(0, 200) + "..." : str;
    }

    /// <summary>
    /// Gets a boolean value from an environment variable.
    /// </summary>
    public static bool GetBooleanFromEnv(string variableName, bool defaultValue = false)
    {
        var value = Environment.GetEnvironmentVariable(variableName);

        if (string.IsNullOrWhiteSpace(value))
        {
            return defaultValue;
        }

        return string.Equals(value.Trim(), "true", StringComparison.OrdinalIgnoreCase)
            || string.Equals(value.Trim(), "1", StringComparison.OrdinalIgnoreCase);
    }
}
