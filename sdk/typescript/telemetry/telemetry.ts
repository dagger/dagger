import { NodeSDK } from "@opentelemetry/sdk-node"
import { SEMRESATTRS_SERVICE_NAME } from "@opentelemetry/semantic-conventions"
import { Resource } from "@opentelemetry/resources"
import * as opentelemetry from "@opentelemetry/api"
import { SpanStatusCode } from "@opentelemetry/api"

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
class DaggerOtelConfigurator {
  private is_configured = false

  private is_enabled = otelEnabled()

  private sdk?: NodeSDK

  /**
   * Initialize the Open Telemetry SDK if enabled or not already configured.
   */
  initialize() {
    if (this.is_configured || !this.is_enabled) {
      return
    }

    console.debug("Initialiazing telemetry")
    this.configure()

    // Create the node SDK with the context manager, the resource and the exporter.
    const sdk = new NodeSDK({
      resource: new Resource({
        [SEMRESATTRS_SERVICE_NAME]: "dagger-typescript-sdk",
      }),
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

  configure() {
    if (!otelConfigured()) {
      console.debug("Telemetry not configured")
      return
    }

    if (!otelEnabled()) {
      console.debug("Telemetry disabled")
      return
    }

    this.setupEnv()
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
    const endpoints = [
      OtelEnv.OTEL_EXPORTER_OTLP_ENDPOINT,
      OtelEnv.OTEL_EXPORTER_OTLP_METRICS_ENDPOINT,
      OtelEnv.OTEL_EXPORTER_OTLP_LOGS_ENDPOINT,
      OtelEnv.OTEL_EXPORTER_OTLP_TRACES_ENDPOINT,
    ]

    endpoints.forEach((endpoint) => {
      const env = process.env[endpoint]
      if (env && env.startsWith("http://")) {
        process.env[OtelEnv.OTEL_EXPORTER_OTLP_INSECURE] = "true"
      }
    })
  }
}

const configurator = new DaggerOtelConfigurator()

export function initiliaze() {
  configurator.initialize()
}

export async function close() {
  await configurator.close()
}

/**
 * Return a tracer to use with Dagger.
 *
 * The tracer is automatically initialized if not already done.
 * As a conveniance function, you can use `withTracingSpan` that automatically close
 * the span at the end of the function.
 */
export function getTracer(): opentelemetry.Tracer {
  initiliaze()

  const tracer = opentelemetry.trace
    .getTracerProvider()
    .getTracer("dagger-typescript-sdk")

  return tracer
}

export function initContext() {
  const ctx = opentelemetry.context.active()

  const parentID = process.env.TRACEPARENT
  if (parentID) {
    return opentelemetry.propagation.extract(ctx, {
      traceparent: parentID,
    })
  }

  return ctx
}

/**
 * Execute the functions with a custom span with the given name using startActiveSpan.
 * The function executed will use the parent context of the function (it can be another span
 * or the main function).
 *
 * @param name The name of the span
 * @param fn The functions to execute
 *
 * WithTracingSpan returns the result of the executed functions.
 *
 * The span is automatically ended when the function is done.
 * The span is automatically marked as an error if the function throws an error.
 *
 *
 * @example
 * ```
 * return withTracingSpan(name, async () => {
 *   return this.containerEcho("test").stdout()
 * })
 * ```
 */
export async function withTracingSpan<T>(
  name: string,
  fn: (span: opentelemetry.Span) => Promise<T>,
): Promise<T> {
  return await opentelemetry.context.with(
    opentelemetry.context.active(),
    async () => {
      return getTracer().startActiveSpan(name, async (span) => {
        try {
          return await fn(span)
        } catch (e) {
          if (e instanceof Error) {
            span.recordException(e)
            span.setStatus({
              code: SpanStatusCode.ERROR,
              message: e.message,
            })
          }
          throw e
        } finally {
          span.end()
        }
      })
    },
  )
}

// This function is used to force flush the spans before the process exits
// A workaround until shutdown works properly
//
// See: https://github.com/open-telemetry/opentelemetry-js/commit/34387774caaa15307e8586206f1ca2e6df96605f
// export async function forceFlush() {
//   const traceProvider = opentelemetry.trace.getTracerProvider()
//   if (traceProvider instanceof NodeTracerProvider) {
//     await traceProvider.forceFlush()
//   } else if (traceProvider instanceof opentelemetry.ProxyTracerProvider) {
//     const delegateProvider = traceProvider.getDelegate()
//     if (delegateProvider instanceof NodeTracerProvider) {
//       await delegateProvider.activeSpanProcessor.forceFlush()
//     }
//   }
// }
