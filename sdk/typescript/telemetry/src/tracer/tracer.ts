import * as opentelemetry from "@opentelemetry/api"

/**
 * Tracer encapsulates the OpenTelemetry Tracer.
 */
export class Tracer {
  private tracer: opentelemetry.Tracer

  constructor(name: string) {
    this.tracer = opentelemetry.trace.getTracer(name)
  }

  public startSpan(
    name: string,
    attributes?: opentelemetry.Attributes,
  ): opentelemetry.Span {
    return this.tracer.startSpan(name, { attributes })
  }

  /**
   * Execute the functions with a custom span with the given name using startActiveSpan.
   * The function executed will use the parent context of the function (it can be another span
   * or the main function).
   *
   * @param name The name of the span
   * @param fn The functions to execute
   *
   * startActiveSpan returns the result of the executed functions.
   *
   * The span is automatically ended when the function is done.
   * The span is automatically marked as an error if the function throws an error.
   *
   *
   * @example
   * ```
   * return getTracer().startActiveSpan(name, async () => {
   *   return this.containerEcho("test").stdout()
   * })
   * ```
   */
  public async startActiveSpan<T>(
    name: string,
    fn: (span: opentelemetry.Span) => Promise<T>,
    attributes?: opentelemetry.Attributes,
  ) {
    return this.tracer.startActiveSpan(name, { attributes }, async (span) => {
      try {
        return await fn(span)
      } catch (e) {
        if (e instanceof Error) {
          span.recordException(e)
          span.setStatus({
            code: opentelemetry.SpanStatusCode.ERROR,
            message: e.message,
          })
        }

        throw e
      } finally {
        span.end()
      }
    })
  }
}
