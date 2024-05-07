import { getEnvWithoutDefaults } from "@opentelemetry/core"
import { NodeSDK } from "@opentelemetry/sdk-node"

const SERVICE_NAME = "dagger-typescript-sdk"

const env = getEnvWithoutDefaults()

/*
 * Look for variables prefixed with OTEL to see if OpenTelemetry is configured.
 */
function otelConfigured(): boolean {
  return Object.keys(process.env).some((key) => key.startsWith("OTEL_"))
}

/**
 * A wrapper around the OpenTelemetry SDK to configure it for Dagger.
 */
export class DaggerOtelConfigurator {
  private is_configured = false

  private sdk?: NodeSDK

  /**
   * Initialize the Open Telemetry SDK if enabled or not already configured.
   */
  initialize() {
    if (this.is_configured) {
      return
    }
    this.configure()
    this.is_configured = true
  }

  configure() {
    if (!otelConfigured()) {
      return
    }

    if (env.OTEL_SDK_DISABLED) {
      return
    }

    this.setupEnv()

    // Create the node SDK with the context manager, the resource and the exporter.
    this.sdk = new NodeSDK({
      serviceName: SERVICE_NAME,
    })

    // Register the SDK to the OpenTelemetry API
    this.sdk.start()
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
    // TODO: The insecure flag should be set by the shim instead of the SDK.
    Object.entries(process.env).forEach(([key, value]) => {
      if (
        key.startsWith("OTEL_") &&
        key.endsWith("_ENDPOINT") &&
        value?.startsWith("http://")
      ) {
        const insecure = key.replace(/_ENDPOINT$/, "_INSECURE")
        if (process.env[insecure] === undefined) {
          process.env[insecure] = "true"
        }
      }
    })
  }
}
