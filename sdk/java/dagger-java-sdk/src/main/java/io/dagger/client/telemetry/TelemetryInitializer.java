package io.dagger.client.telemetry;

import io.opentelemetry.api.OpenTelemetry;
import io.opentelemetry.api.trace.propagation.W3CTraceContextPropagator;
import io.opentelemetry.context.propagation.ContextPropagators;
import io.opentelemetry.exporter.otlp.http.trace.OtlpHttpSpanExporter;
import io.opentelemetry.exporter.otlp.trace.OtlpGrpcSpanExporter;
import io.opentelemetry.sdk.OpenTelemetrySdk;
import io.opentelemetry.sdk.resources.Resource;
import io.opentelemetry.sdk.trace.SdkTracerProvider;
import io.opentelemetry.sdk.trace.export.BatchSpanProcessor;
import io.opentelemetry.sdk.trace.export.SpanExporter;
import java.util.concurrent.TimeUnit;
import org.apache.commons.lang3.StringUtils;

public class TelemetryInitializer {

  private static final String SERVICE_NAME = "dagger-java-sdk";
  private static final String OTLP_DISABLED = System.getenv("OTEL_SDK_DISABLED");
  private static final String OTLP_ENDPOINT = System.getenv("OTEL_EXPORTER_OTLP_ENDPOINT");
  private static final String OTLP_PROTOCOL = System.getenv("OTEL_EXPORTER_OTLP_PROTOCOL");

  private static OpenTelemetry INSTANCE;

  static OpenTelemetry init() {
    if (INSTANCE != null) {
      return INSTANCE;
    }

    if (OTLP_DISABLED == "TRUE"
        || StringUtils.isBlank(OTLP_ENDPOINT)
        || (!OTLP_ENDPOINT.startsWith("http://") && !OTLP_ENDPOINT.startsWith("https://"))) {
      return OpenTelemetry.noop();
    }

    Resource resource =
        Resource.getDefault().merge(Resource.builder().put("serviceName", SERVICE_NAME).build());

    SpanExporter spanExporter;
    if (OTLP_PROTOCOL.contains("grpc")) {
      spanExporter =
          OtlpGrpcSpanExporter.builder()
              .setEndpoint(OTLP_ENDPOINT)
              .setTimeout(2, TimeUnit.SECONDS)
              .build();
    } else {
      spanExporter =
          OtlpHttpSpanExporter.builder()
              .setEndpoint(OTLP_ENDPOINT)
              .setTimeout(2, TimeUnit.SECONDS)
              .build();
    }

    SdkTracerProvider sdkTracerProvider =
        SdkTracerProvider.builder()
            .setResource(resource)
            .addSpanProcessor(
                BatchSpanProcessor.builder(spanExporter)
                    .setScheduleDelay(100, TimeUnit.MILLISECONDS)
                    .build())
            .build();

    OpenTelemetrySdk sdk =
        OpenTelemetrySdk.builder()
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
