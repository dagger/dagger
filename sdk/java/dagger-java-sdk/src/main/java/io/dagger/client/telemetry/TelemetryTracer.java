package io.dagger.client.telemetry;

import java.util.function.Supplier;

import io.opentelemetry.api.OpenTelemetry;
import io.opentelemetry.api.common.Attributes;
import io.opentelemetry.api.trace.Span;
import io.opentelemetry.api.trace.StatusCode;
import io.opentelemetry.api.trace.Tracer;

public class TelemetryTracer {

    private Tracer tracer;

    public TelemetryTracer(OpenTelemetry openTelemetry, String name) {
        this.tracer = openTelemetry.getTracer(name);
    }

    public Span startSpan(String name, Attributes attributes) {
        return tracer.spanBuilder(name).setAllAttributes(attributes).startSpan();
    }

    public <T> T startActiveSpan(String name, Attributes attributes, Supplier<T> function) {
        Span span = tracer.spanBuilder(name).setAllAttributes(attributes).startSpan();

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
