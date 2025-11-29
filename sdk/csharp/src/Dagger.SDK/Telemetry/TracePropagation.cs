namespace Dagger.Telemetry;

/// <summary>
/// Handles W3C Trace Context propagation for distributed tracing.
/// Extracts TRACEPARENT from environment variable and makes it available
/// for propagation to the Dagger engine.
/// </summary>
internal static class TracePropagation
{
    private static string? _traceParent;
    private static bool _initialized;
    private static readonly object InitLock = new();

    /// <summary>
    /// Initializes trace propagation by extracting TRACEPARENT from environment.
    /// This should be called early in the module runtime lifecycle.
    /// </summary>
    public static void Initialize()
    {
        if (_initialized)
        {
            return;
        }

        lock (InitLock)
        {
            if (_initialized)
            {
                return;
            }

            _traceParent = Environment.GetEnvironmentVariable("TRACEPARENT");
            _initialized = true;
        }
    }

    /// <summary>
    /// Gets the TRACEPARENT value for propagation, or null if not set.
    /// </summary>
    public static string? GetTraceParent()
    {
        Initialize();
        return _traceParent;
    }

    /// <summary>
    /// Resets the trace propagation state. This is intended for testing purposes only.
    /// </summary>
    internal static void Reset()
    {
        lock (InitLock)
        {
            _initialized = false;
            _traceParent = null;
        }
    }
}
