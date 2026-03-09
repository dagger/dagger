using System.Diagnostics;
using OpenTelemetry.Trace;

namespace Dagger.Telemetry;

/// <summary>
/// Tracer wrapper for creating and managing OpenTelemetry spans.
/// Provides helper methods for function invocation tracing with automatic exception recording.
/// </summary>
internal static class DaggerTracer
{
    /// <summary>
    /// Starts an active span, executes the function, and handles exceptions automatically.
    /// The span is ended automatically when the function completes.
    /// </summary>
    /// <typeparam name="T">The return type of the function.</typeparam>
    /// <param name="name">The name of the span (typically the function name).</param>
    /// <param name="fn">The function to execute within the span context.</param>
    /// <param name="attributes">Optional attributes to add to the span.</param>
    /// <returns>The result of the function execution.</returns>
    public static async Task<T> StartActiveSpanAsync<T>(
        string name,
        Func<Activity?, Task<T>> fn,
        IDictionary<string, object?>? attributes = null
    )
    {
        using var activity = DaggerTelemetryInitializer.ActivitySource.StartActivity(name);

        if (activity != null && attributes != null)
        {
            foreach (var (key, value) in attributes)
            {
                if (value != null)
                {
                    activity.SetTag(key, value);
                }
            }
        }

        try
        {
            var result = await fn(activity);

            if (activity != null)
            {
                activity.SetStatus(ActivityStatusCode.Ok);
            }

            return result;
        }
        catch (Exception ex)
        {
            if (activity != null)
            {
                RecordException(activity, ex);
                activity.SetStatus(ActivityStatusCode.Error, ex.Message);
            }
            throw;
        }
    }

    /// <summary>
    /// Starts an active span for synchronous operations.
    /// </summary>
    public static T StartActiveSpan<T>(
        string name,
        Func<Activity?, T> fn,
        IDictionary<string, object?>? attributes = null
    )
    {
        using var activity = DaggerTelemetryInitializer.ActivitySource.StartActivity(name);

        if (activity != null && attributes != null)
        {
            foreach (var (key, value) in attributes)
            {
                if (value != null)
                {
                    activity.SetTag(key, value);
                }
            }
        }

        try
        {
            var result = fn(activity);

            if (activity != null)
            {
                activity.SetStatus(ActivityStatusCode.Ok);
            }

            return result;
        }
        catch (Exception ex)
        {
            if (activity != null)
            {
                RecordException(activity, ex);
                activity.SetStatus(ActivityStatusCode.Error, ex.Message);
            }
            throw;
        }
    }

    /// <summary>
    /// Records an exception on the span using OpenTelemetry semantic conventions.
    /// </summary>
    /// <param name="activity">The activity to record the exception on.</param>
    /// <param name="exception">The exception to record.</param>
    private static void RecordException(Activity activity, Exception exception)
    {
        var exceptionType = exception.GetType().FullName ?? exception.GetType().Name;
        var exceptionMessage = exception.Message;
        var exceptionStackTrace = exception.ToString(); // Full stack trace with inner exceptions

        // Using OpenTelemetry semantic conventions for exception attributes
        activity.SetTag("exception.type", exceptionType);
        activity.SetTag("exception.message", exceptionMessage);
        activity.SetTag("exception.stacktrace", exceptionStackTrace);

        // Record as an event as well (standard OTel pattern)
        var tags = new ActivityTagsCollection
        {
            { "exception.type", exceptionType },
            { "exception.message", exceptionMessage },
            { "exception.stacktrace", exceptionStackTrace },
        };

        activity.AddEvent(new ActivityEvent("exception", DateTimeOffset.UtcNow, tags));
    }
}
