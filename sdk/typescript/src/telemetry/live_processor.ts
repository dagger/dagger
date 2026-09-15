import type { Context } from "@opentelemetry/api"
import { BatchSpanProcessor, type Span } from "@opentelemetry/sdk-trace-base"

/**
 * Live span processor implementation.
 *
 * It's a BatchSpanProcessor whose on_start calls on_end on the underlying
 * SpanProcessor in order to send live telemetry.
 */
export class LiveProcessor extends BatchSpanProcessor {
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  override onStart(_span: Span, _parentContext: Context): void {
    this.onEnd(_span)
  }
}
