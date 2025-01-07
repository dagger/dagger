import * as opentelemetry from "@opentelemetry/api"

/**
 * runWithSpan is a proxy function to wrap a function within a specific span.
 *
 * Note: This function should only be used by the generated client.
 * We execute the logic here to avoid dealing with dependency conflicts by directly
 * importing the opentelemetry package inside the generated client, instead we proxy it there.
 */
export async function runWithSpan<T, S>(
  fn: (span: S) => Promise<T>,
  parentSpan: S,
  startSpan: S,
  spanIdHex: string,
): Promise<T> {
  const currentSpan =
    opentelemetry.trace.getSpan(opentelemetry.context.active()) || undefined
  const currentSpanContext = currentSpan?.spanContext()

  if (!currentSpanContext) {
    return await fn(parentSpan)
  }

  // Extract trace ID and other fields
  const traceId = currentSpanContext.traceId
  const traceFlags = currentSpanContext.traceFlags
  const traceState = currentSpanContext.traceState

  // Construct the new SpanContext
  const newSpanContext: opentelemetry.SpanContext = {
    traceId,
    spanId: spanIdHex,
    traceFlags,
    isRemote: true,
    traceState,
  }

  // Bind the new context
  const newContext = opentelemetry.trace.setSpan(
    opentelemetry.context.active(),
    opentelemetry.trace.wrapSpanContext(newSpanContext),
  )

  return await opentelemetry.context.with(newContext, fn, parentSpan, startSpan)
}
