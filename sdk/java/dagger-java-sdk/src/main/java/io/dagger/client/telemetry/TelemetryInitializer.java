package io.dagger.client.telemetry;

import java.util.concurrent.TimeUnit;

import io.opentelemetry.api.OpenTelemetry;
import io.opentelemetry.api.trace.propagation.W3CTraceContextPropagator;
import io.opentelemetry.context.propagation.ContextPropagators;
import io.opentelemetry.exporter.otlp.http.trace.OtlpHttpSpanExporter;
import io.opentelemetry.sdk.OpenTelemetrySdk;
import io.opentelemetry.sdk.resources.Resource;
import io.opentelemetry.sdk.trace.SdkTracerProvider;
import io.opentelemetry.sdk.trace.export.BatchSpanProcessor;

public class TelemetryInitializer {

    private static final String SERVICE_NAME = "dagger-java-sdk";

    private static OpenTelemetry INSTANCE;

    static OpenTelemetry init() {
        if (INSTANCE != null) {
            return INSTANCE;
        }

        if (System.getenv("OTEL_SDK_DISABLED") == "TRUE") {
            return OpenTelemetry.noop();
        }

        // TODO put otel configuration by env var or inherit from another context?
        // support OtlpHttp or OtlpGrpc
        Resource resource = Resource.getDefault()
                .merge(Resource.builder().put("serviceName", SERVICE_NAME).build());

        SdkTracerProvider sdkTracerProvider = SdkTracerProvider.builder()
                .setResource(resource)
                .addSpanProcessor(
                        BatchSpanProcessor.builder(OtlpHttpSpanExporter.builder()
                                .setTimeout(2, TimeUnit.SECONDS)
                                .build())
                                .setScheduleDelay(100, TimeUnit.MILLISECONDS)
                                .build())
                .build();

        OpenTelemetrySdk sdk = OpenTelemetrySdk.builder()
                .setTracerProvider(sdkTracerProvider)
                .setPropagators(ContextPropagators.create(W3CTraceContextPropagator.getInstance()))
                .build();

        Runtime.getRuntime().addShutdownHook(new Thread(sdkTracerProvider::close));

        INSTANCE = sdk;

        return sdk;
    }

    static void close() {
        if (INSTANCE != null) {
            ((SdkTracerProvider) INSTANCE.getTracerProvider()).close();
        }
    }
}
