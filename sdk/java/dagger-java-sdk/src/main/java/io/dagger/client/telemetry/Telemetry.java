package io.dagger.client.telemetry;

public class Telemetry {

    public TelemetryTracer getTracer(String name) {
        TelemetryInitializer.init();
        return new TelemetryTracer(TelemetryInitializer.init(), name);
    }

    public void initialize() {
        TelemetryInitializer.init();
    }

    public void close() {
        TelemetryInitializer.init();
    }
}
