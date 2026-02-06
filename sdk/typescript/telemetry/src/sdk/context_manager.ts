import { propagation, ROOT_CONTEXT } from "@opentelemetry/api"
import { AsyncLocalStorageContextManager } from "@opentelemetry/context-async-hooks"

/**
 * Context manager to automatically add `TRACEPARENT` when using the dagger otel
 * instrumentation library.
 */
export class DaggerContextManager extends AsyncLocalStorageContextManager {
  override active() {
    const ctx = super.active()

    if (ctx === ROOT_CONTEXT) {
      return propagation.extract(ROOT_CONTEXT, {
        traceparent: process.env.TRACEPARENT,
      })
    }

    return ctx
  }
}
