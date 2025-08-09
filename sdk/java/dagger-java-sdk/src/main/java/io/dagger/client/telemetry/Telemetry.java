package io.dagger.client.telemetry;

import io.opentelemetry.api.common.Attributes;
import io.opentelemetry.api.trace.Span;
import io.opentelemetry.api.trace.SpanContext;
import io.opentelemetry.api.trace.TraceFlags;
import io.opentelemetry.api.trace.TraceState;
import io.opentelemetry.context.Context;
import io.opentelemetry.context.Scope;

public class Telemetry implements AutoCloseable {

  private static final String TRACER_NAME = "dagger.io/sdk.java";

  @Override
  public void close() throws Exception {
    TelemetryInitializer.close();
  }

  public <T> void trace(String name, Attributes attributes, TelemetrySupplier<T> supplier)
      throws Exception {
    TelemetryTracer tracer = new TelemetryTracer(TelemetryInitializer.init(), TRACER_NAME);
    try (Scope ignored = getContext().makeCurrent()) {
      tracer.startActiveSpan(name, attributes, supplier);
    }
  }

  private Context getContext() {
    Context ctx = Context.current();

    if (Span.current() != null && Span.current().getSpanContext().isValid()) {
      return ctx;
    }

    String traceparent = System.getenv("TRACEPARENT");
    if (traceparent == null || traceparent.isBlank()) {
      return ctx;
    }

    try {
      String[] parts = traceparent.split("-");
      if (parts.length != 4) {
        return ctx;
      }

      String traceId = parts[1];
      String spanId = parts[2];
      String traceFlags = parts[3];

      SpanContext remoteContext =
          SpanContext.createFromRemoteParent(
              traceId, spanId, TraceFlags.fromHex(traceFlags, 0), TraceState.getDefault());

      return ctx.with(Span.wrap(remoteContext));
    } catch (Exception e) {
      System.err.println("Failed to parse TRACEPARENT: " + e.getMessage());
      return ctx;
    }
  }
}
