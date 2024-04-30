import * as opentelemetry from "@opentelemetry/api"

import { DaggerOtelConfigurator } from "./init.js"
import { Tracer } from "./tracer.js"

const DAGGER_TRACER_NAME = "dagger.io/sdk.typescript"

const configurator = new DaggerOtelConfigurator()

export function initialize() {
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
 *
 * You can add a custom name to the tracer based on your application.
 */
export function getTracer(name = DAGGER_TRACER_NAME): Tracer {
  initialize()
  return new Tracer(name)
}

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
