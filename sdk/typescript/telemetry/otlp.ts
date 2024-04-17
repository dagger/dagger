import { NodeSDK } from "@opentelemetry/sdk-node"
import { SEMRESATTRS_SERVICE_NAME } from "@opentelemetry/semantic-conventions"
import { Resource } from "@opentelemetry/resources"
import {
  BatchSpanProcessor,
  NodeTracerProvider,
} from "@opentelemetry/sdk-trace-node"
import { OTLPTraceExporter } from "@opentelemetry/exporter-trace-otlp-grpc"
import * as opentelemetry from "@opentelemetry/api"
import { SpanStatusCode } from "@opentelemetry/api"
import { credentials } from "@grpc/grpc-js"

// Initialiaze the OTLP exporter, it takes his configuration from
// the environment variables prefixed by "OTLP_"
const exporter = new OTLPTraceExporter({
  credentials: credentials.createInsecure(),
})

// Create the node SDK with the context manager, the resource and the exporter.
const sdk = new NodeSDK({
  resource: new Resource({
    [SEMRESATTRS_SERVICE_NAME]: "dagger-typescript-sdk",
  }),
})

// Register the SDK to the OpenTelemetry API
sdk.start()

// The TypeScript SDK tracer to use globally.
export const tracer = opentelemetry.trace
  .getTracerProvider()
  .getTracer("dagger.io/sdk.typescript")

/**
 * Returns the context based on the current active context or the TRACEPARENT
 * environment variable.
 */
export function getContext() {
  const ctx = opentelemetry.context.active()
  const spanCtx = opentelemetry.trace.getSpanContext(ctx)

  if (spanCtx && opentelemetry.trace.isSpanContextValid(spanCtx)) {
    return ctx
  }

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
  return await opentelemetry.context.with(getContext(), async () => {
    return tracer.startActiveSpan(name, async (span) => {
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
  })
}

// This function is used to force flush the spans before the process exits
// A workaround until shutdown works properly
//
// See: https://github.com/open-telemetry/opentelemetry-js/commit/34387774caaa15307e8586206f1ca2e6df96605f
export async function forceFlush() {
  const traceProvider = opentelemetry.trace.getTracerProvider()
  if (traceProvider instanceof NodeTracerProvider) {
    await traceProvider.forceFlush()
  } else if (traceProvider instanceof opentelemetry.ProxyTracerProvider) {
    const delegateProvider = traceProvider.getDelegate()
    if (delegateProvider instanceof NodeTracerProvider) {
      await delegateProvider.activeSpanProcessor.forceFlush()
    }
  }
}
