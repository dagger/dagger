package io.dagger.client.telemetry;

import io.opentelemetry.api.trace.Span;
import io.opentelemetry.api.trace.SpanContext;
import io.opentelemetry.api.trace.TraceFlags;
import io.opentelemetry.api.trace.TraceState;
import io.opentelemetry.context.Context;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

public class Telemetry implements AutoCloseable {

  private static final Logger LOG = LoggerFactory.getLogger(Telemetry.class);

  public Telemetry() {
    TelemetryInitializer.init();
    getContext().makeCurrent();
  }

  @Override
  public void close() throws Exception {
    TelemetryInitializer.close();
  }

  private Context getContext() {
    LOG.debug("Retrieving context");
    Context ctx = Context.current();

    if (Span.current() != null && Span.current().getSpanContext().isValid()) {
      LOG.debug("Current context is valid");
      return ctx;
    }

    String traceparent = System.getenv("TRACEPARENT");
    if (traceparent == null || traceparent.isBlank()) {
      LOG.debug("Current context is valid, traceparent don't exists");
      return ctx;
    }

    try {
      LOG.debug("Retrieving remote context");

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
      LOG.error("Telemetry failed to parse TRACEPARENT: {}", e.getMessage(), e);
      return ctx;
    }
  }
}
