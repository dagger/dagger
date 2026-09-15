package io.dagger.client.telemetry;

import io.opentelemetry.api.OpenTelemetry;
import io.opentelemetry.api.common.Attributes;
import io.opentelemetry.api.trace.Span;
import io.opentelemetry.api.trace.StatusCode;
import io.opentelemetry.api.trace.Tracer;
import io.opentelemetry.context.Context;

public class TelemetryTracer {

  private Tracer tracer;

  public TelemetryTracer(OpenTelemetry openTelemetry, String name) {
    this.tracer = openTelemetry.getTracer(name);
  }

  public <T> T startActiveSpan(
      String name, Context context, Attributes attributes, TelemetrySupplier<T> function)
      throws Exception {
    Span span =
        tracer.spanBuilder(name).setParent(context).setAllAttributes(attributes).startSpan();

    try (var scope = span.makeCurrent()) {
      return function.get();
    } catch (Exception e) {
      span.recordException(e);
      span.setStatus(StatusCode.ERROR, e.getMessage());
      throw e;
    } finally {
      span.end();
    }
  }
}
