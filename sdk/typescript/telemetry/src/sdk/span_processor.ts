import type { Context } from "@opentelemetry/api"
import type {
  BufferConfig,
  Span,
  SpanExporter,
} from "@opentelemetry/sdk-trace-base"
import { BatchSpanProcessor } from "@opentelemetry/sdk-trace-base"

/**
 * Batch span processor scheduler delays.
 * We set it to 100ms so it's almost live.
 */
export const NEARLY_IMMEDIATE = 100

/**
 * Live span processor implementation.
 *
 * It's a BatchSpanProcessor whose on_start calls on_end on the underlying
 * SpanProcessor in order to send live telemetry.
 */
export class LiveSpanProcessor extends BatchSpanProcessor {
  constructor(exporter: SpanExporter, config?: BufferConfig) {
    if (!config) {
      config = {
        scheduledDelayMillis: NEARLY_IMMEDIATE,
      }
    }

    if (config?.scheduledDelayMillis === undefined) {
      config.scheduledDelayMillis = NEARLY_IMMEDIATE
    }

    super(exporter, config)
  }

  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  override onStart(_span: Span, _parentContext: Context): void {
    this.onEnd(_span)
  }
}
