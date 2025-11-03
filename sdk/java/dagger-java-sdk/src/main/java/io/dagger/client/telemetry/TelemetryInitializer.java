package io.dagger.client.telemetry;

import io.opentelemetry.api.GlobalOpenTelemetry;
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
import java.util.Optional;
import java.util.concurrent.TimeUnit;
import org.apache.commons.lang3.Strings;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

public class TelemetryInitializer {

  private static final Logger LOG = LoggerFactory.getLogger(TelemetryInitializer.class);
  private static final String SERVICE_NAME = "dagger-java-sdk";
  private static final String OTLP_DISABLED = System.getenv("OTEL_SDK_DISABLED");
  private static final String OTLP_ENDPOINT = System.getenv("OTEL_EXPORTER_OTLP_ENDPOINT");
  private static final String OTLP_TRACES_ENDPOINT =
      System.getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT");
  private static final String OTLP_PROTOCOL = System.getenv("OTEL_EXPORTER_OTLP_PROTOCOL");

  private static OpenTelemetrySdk INSTANCE;

  static OpenTelemetry init() {
    if (INSTANCE != null) {
      return INSTANCE;
    }

    LOG.debug("Initializing Telemetry");

    if (Strings.CI.equals(OTLP_DISABLED, "TRUE")) {
      LOG.info("Opentelemetry is disabled");
      return OpenTelemetry.noop();
    }

    if (!Strings.CS.startsWith(OTLP_ENDPOINT, "http://")
        && !Strings.CS.startsWith(OTLP_ENDPOINT, "https://")
        && !Strings.CS.startsWith(OTLP_TRACES_ENDPOINT, "http://")
        && !Strings.CS.startsWith(OTLP_TRACES_ENDPOINT, "https://")) {
      LOG.warn("Opentelemetry configuration is not valid, please check!");
      return OpenTelemetry.noop();
    }

    Resource resource =
        Resource.getDefault().merge(Resource.builder().put("serviceName", SERVICE_NAME).build());

    SpanExporter spanExporter;
    if (Strings.CI.equals(OTLP_PROTOCOL, "http/protobuf")) {
      spanExporter =
          OtlpHttpSpanExporter.builder()
              .setEndpoint(Optional.ofNullable(OTLP_TRACES_ENDPOINT).orElse(OTLP_ENDPOINT))
              .setTimeout(2, TimeUnit.SECONDS)
              .build();
    } else {
      spanExporter =
          OtlpGrpcSpanExporter.builder()
              .setEndpoint(Optional.ofNullable(OTLP_TRACES_ENDPOINT).orElse(OTLP_ENDPOINT))
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

    // Configure sdk as global instance for opentelemetry
    GlobalOpenTelemetry.set(sdk);
    LOG.debug("GlobalTelemetry initialized successfully {}", sdk);

    INSTANCE = sdk;

    return sdk;
  }

  static void close() {
    if (INSTANCE != null) {
      INSTANCE.close();
    }
  }
}
