using System.Diagnostics;
using OpenTelemetry;
using OpenTelemetry.Exporter;
using OpenTelemetry.Resources;
using OpenTelemetry.Trace;

namespace Dagger.Telemetry;

/// <summary>
/// Singleton configurator for OpenTelemetry SDK initialization.
/// Handles environment-based configuration, exporter setup, and tracer provider management.
/// </summary>
internal static class DaggerTelemetryInitializer
{
    private static readonly object InitLock = new();
    private static bool _isConfigured;
    private static TracerProvider? _tracerProvider;
    private static ActivitySource? _activitySource;

    private const string ServiceName = "dagger-csharp-sdk";
    private const string TracerName = "dagger.io/sdk.csharp";

    // OTEL_SDK_DISABLED is the only variable we check manually
    // All other OTEL_EXPORTER_OTLP_* variables are automatically handled by OtlpExporterOptions
    private const string OtelSdkDisabled = "OTEL_SDK_DISABLED";

    /// <summary>
    /// Gets the configured ActivitySource for creating spans.
    /// </summary>
    public static ActivitySource ActivitySource
    {
        get
        {
            Initialize();
            return _activitySource ??= new ActivitySource(TracerName);
        }
    }

    /// <summary>
    /// Initializes the OpenTelemetry SDK if not already configured.
    /// </summary>
    public static void Initialize()
    {
        if (_isConfigured)
        {
            return;
        }

        lock (InitLock)
        {
            if (_isConfigured)
            {
                return;
            }

            Configure();
            _isConfigured = true;
        }
    }

    /// <summary>
    /// Configures the OpenTelemetry SDK based on environment variables.
    /// </summary>
    private static void Configure()
    {
        // Check if telemetry is disabled
        if (IsOtelDisabled())
        {
            _activitySource = new ActivitySource(TracerName);
            return;
        }

        // Check if any OTEL configuration exists
        if (!IsOtelConfigured())
        {
            _activitySource = new ActivitySource(TracerName);
            return;
        }

        // Service name can be overridden by OTEL_SERVICE_NAME or OTEL_RESOURCE_ATTRIBUTES
        var serviceName = Environment.GetEnvironmentVariable("OTEL_SERVICE_NAME") ?? ServiceName;

        var builder = Sdk.CreateTracerProviderBuilder()
            .AddSource(TracerName)
            .ConfigureResource(r => r.AddService(serviceName));

        // AddOtlpExporter() automatically reads these environment variables:
        // - OTEL_EXPORTER_OTLP_ENDPOINT or OTEL_EXPORTER_OTLP_TRACES_ENDPOINT
        // - OTEL_EXPORTER_OTLP_PROTOCOL or OTEL_EXPORTER_OTLP_TRACES_PROTOCOL
        // - OTEL_EXPORTER_OTLP_HEADERS or OTEL_EXPORTER_OTLP_TRACES_HEADERS
        // - OTEL_EXPORTER_OTLP_TIMEOUT or OTEL_EXPORTER_OTLP_TRACES_TIMEOUT
        builder.AddOtlpExporter();

        _tracerProvider = builder.Build();
        _activitySource = new ActivitySource(TracerName);
    }

    /// <summary>
    /// Shuts down the tracer provider and flushes pending spans.
    /// </summary>
    public static async Task ShutdownAsync()
    {
        if (_tracerProvider != null)
        {
            _tracerProvider.ForceFlush();
            await Task.Run(() => _tracerProvider.Dispose());
        }
    }

    /// <summary>
    /// Checks if OpenTelemetry SDK is disabled via environment variable.
    /// </summary>
    private static bool IsOtelDisabled()
    {
        var disabled = Environment.GetEnvironmentVariable(OtelSdkDisabled);
        return string.Equals(disabled?.Trim(), "true", StringComparison.OrdinalIgnoreCase);
    }

    /// <summary>
    /// Checks if any OpenTelemetry configuration exists in environment variables.
    /// </summary>
    private static bool IsOtelConfigured()
    {
        return Environment
            .GetEnvironmentVariables()
            .Keys.Cast<string>()
            .Any(key => key.StartsWith("OTEL_", StringComparison.OrdinalIgnoreCase));
    }
}
