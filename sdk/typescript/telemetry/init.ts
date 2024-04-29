import { NodeSDK } from "@opentelemetry/sdk-node"
import { OtelEnv } from "./environment.js"

const SERVICE_NAME = "dagger-typescript-sdk"

/*
 * Look for variables prefixed with OTEL to see if OTEL is enabled
 */
function otelConfigured(): boolean {
  return Object.keys(process.env).some((key) => key.startsWith("OTEL_"))
}

/**
 * Check if SDK is enabled, according to the current OTLP SDK node, it's only possible to disable
 * the whole SDK.
 *
 * See: https://github.com/open-telemetry/opentelemetry-js/blob/3ab4f765d8d696327b7d139ae6a45e7bd7edd924/experimental/packages/opentelemetry-sdk-node/README.md#L164
 */
function otelEnabled(name?: string): boolean {
  if (name) {
    name = `OTEL_TYPESCRIPT_${name.toUpperCase()}_INSTRUMENTATION_DISABLED`
  }

  if (!name) {
    name = OtelEnv.OTEL_SDK_DISABLED
  }

  return process.env[name] !== "true"
}

/**
 * A wrapper around the OpenTelemetry SDK to configure it for Dagger.
 */
export class DaggerOtelConfigurator {
  private is_configured = false

  private is_enabled = false

  private sdk?: NodeSDK

  /**
   * Initialize the Open Telemetry SDK if enabled or not already configured.
   */
  initialize() {
    if (!otelConfigured()) {
      console.debug("Telemetry not configured")
      return
    }

    if (!otelEnabled()) {
      console.debug("Telemetry disabled")
      return
    } else {
      this.is_enabled = true
    }

    if (this.is_configured || !this.is_enabled) {
      return
    }

    console.debug("Initializing telemetry")
    this.setupEnv()

    // Create the node SDK with the context manager, the resource and the exporter.
    const sdk = new NodeSDK({
      serviceName: SERVICE_NAME,
    })

    // Register the SDK to the OpenTelemetry API if OTEL is enabled
    sdk.start()
    console.debug("Telemetry initialized")
    this.is_configured = true
    this.sdk = sdk
  }

  /**
   * Shutdown the Open Telemetry SDK to flush traces and metrics and close the connection.
   */
  async close() {
    if (this.sdk) {
      this.sdk.shutdown()
    }
  }

  /**
   * Setup environment for auto-configuring the SDK.
   */
  setupEnv() {
    // Setup a default value for the service name.
    if (!process.env[OtelEnv.OTEL_SERVICE_NAME]) {
      process.env[OtelEnv.OTEL_SERVICE_NAME] = SERVICE_NAME
    }

    // TODO: The insecure flag should be set by the shim instead of the SDK.
    const endpoints = {
      [OtelEnv.OTEL_EXPORTER_OTLP_ENDPOINT]:
        OtelEnv.OTEL_EXPORTER_OTLP_INSECURE,
      [OtelEnv.OTEL_EXPORTER_OTLP_METRICS_ENDPOINT]:
        OtelEnv.OTEL_EXPORTER_OTLP_METRICS_INSECURE,
      [OtelEnv.OTEL_EXPORTER_OTLP_LOGS_ENDPOINT]:
        OtelEnv.OTEL_EXPORTER_OTLP_LOGS_INSECURE,
      [OtelEnv.OTEL_EXPORTER_OTLP_TRACES_ENDPOINT]:
        OtelEnv.OTEL_EXPORTER_OTLP_TRACES_INSECURE,
    }

    Object.entries(endpoints).forEach(([endpoint, insecure]) => {
      const env = process.env[endpoint]
      if (env && env.startsWith("http://")) {
        process.env[insecure] = "true"
      }
    })
  }
}
