using System.Diagnostics;
using OpenTelemetry;
using OpenTelemetry.Trace;

namespace Dagger.Telemetry;

/// <summary>
/// Custom span processor that exports spans immediately when they start.
/// Enabled by setting OTEL_EXPORTER_OTLP_TRACES_LIVE environment variable.
/// This provides real-time visibility in observability tools like Dagger Cloud.
/// </summary>
internal class LiveSpanProcessor : BaseProcessor<Activity>
{
    private readonly BaseExporter<Activity> _exporter;

    public LiveSpanProcessor(BaseExporter<Activity> exporter)
    {
        _exporter = exporter ?? throw new ArgumentNullException(nameof(exporter));
    }

    public override void OnStart(Activity activity)
    {
        base.OnStart(activity);

        // Export immediately when span starts (live mode)
        // This matches Python and TypeScript SDK behavior
        OnEnd(activity);
    }

    public override void OnEnd(Activity activity)
    {
        if (activity.Recorded)
        {
            _exporter.Export(new Batch<Activity>([activity], 1));
        }
    }

    protected override bool OnForceFlush(int timeoutMilliseconds)
    {
        return _exporter.ForceFlush(timeoutMilliseconds);
    }

    protected override bool OnShutdown(int timeoutMilliseconds)
    {
        return _exporter.Shutdown(timeoutMilliseconds);
    }
}
