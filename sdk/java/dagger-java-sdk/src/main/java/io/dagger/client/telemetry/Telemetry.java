package io.dagger.client.telemetry;

import io.dagger.client.FunctionCall;
import io.dagger.client.FunctionCallArgValue;
import io.dagger.client.JsonConverter;
import io.opentelemetry.api.common.Attributes;
import io.opentelemetry.api.common.AttributesBuilder;
import io.opentelemetry.api.trace.Span;
import io.opentelemetry.api.trace.SpanContext;
import io.opentelemetry.api.trace.TraceFlags;
import io.opentelemetry.api.trace.TraceState;
import io.opentelemetry.context.Context;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

public class Telemetry implements AutoCloseable {

  private static final Logger LOG = LoggerFactory.getLogger(Telemetry.class);
  private static final String TRACER_NAME = "dagger.io/sdk.java";

  private final TelemetryTracer tracer;

  public Telemetry() {
    tracer = new TelemetryTracer(TelemetryInitializer.init(), TRACER_NAME);
    getContext().makeCurrent();
  }

  @Override
  public void close() throws Exception {
    TelemetryInitializer.close();
  }

  public <T> void trace(FunctionCall fnCall, TelemetrySupplier<T> supplier) throws Exception {
    Context ctx = getContext();
    ctx.wrap(() -> tracer.startActiveSpan(fnCall.name(), ctx, getAttributes(fnCall), supplier))
        .call();
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

  private Attributes getAttributes(FunctionCall fnCall) throws Exception {
    AttributesBuilder builder = Attributes.builder();
    for (FunctionCallArgValue arg : fnCall.inputArgs()) {
      builder.put(arg.name(), JsonConverter.fromJSON(arg.value(), String.class));
    }
    return builder.build();
  }
}
