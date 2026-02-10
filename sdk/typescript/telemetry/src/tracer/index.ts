import { Tracer } from "./tracer"

const DAGGER_TRACER_NAME = "dagger.io/sdk.typescript"

/**
 * Return an OpenTelemetry tracer to use with Dagger.
 *
 * As a conveniance function, you can use `startActiveSpan` that automatically close
 * the span at the end of the function.
 *
 * You can add a custom name to the tracer based on your application.
 */
export function getTracer(name = DAGGER_TRACER_NAME): Tracer {
  return new Tracer(name)
}

export type { Tracer } from "./tracer"
