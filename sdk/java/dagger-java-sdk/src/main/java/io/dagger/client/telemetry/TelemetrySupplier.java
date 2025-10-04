package io.dagger.client.telemetry;

@FunctionalInterface
public interface TelemetrySupplier<T> {

  T get() throws Exception;
}
